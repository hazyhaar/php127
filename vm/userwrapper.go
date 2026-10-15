package vm

import (
	"errors"
	"io"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/streams"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// phpWrapperStream sert un flux de protocole utilisateur en relayant Read, Write, Seek, Flush, EOF et Close
// vers les méthodes stream_read, stream_write, stream_seek, stream_tell, stream_flush, stream_eof et stream_close
// de l'instance PHP créée à l'ouverture.
type phpWrapperStream struct {
	vm      *VM
	classID uint32
	obj     types.Value
	buf     []byte
	eof     bool
	closed  bool
}

func (vm *VM) invokeWrapperMethod(classID uint32, obj types.Value, method string, args []types.Value) (types.Value, bool, bool) {
	m, _, ok := vm.findMethod(classID, method)
	if !ok || m.Func == nil {
		vm.releaseArgs(args)
		return types.Value{}, false, false
	}
	pushedRoot := false
	if len(vm.frames) == 0 {
		root := &bytecode.OpArray{
			Name:           "{main}",
			Code:           []bytecode.Instr{{Op: bytecode.OpReturn, Flags: bytecode.FlagReturnVoid}},
			Aux:            []uint32{bytecode.NoCV},
			CVMetaResolved: true,
		}
		if vm.Program != nil && vm.Program.Main != nil {
			root.Filename = vm.Program.Main.Filename
		}
		if !vm.pushFrame(root, 0, 0, types.Value{}, false, 0) {
			vm.releaseArgs(args)
			return types.Value{}, true, true
		}
		pushedRoot = true
	}
	savedControl := vm.control
	savedLine := vm.line
	ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(vm.Zone, obj), true, args, classID, m.ClassID, vm.line)
	if pushedRoot && len(vm.frames) > 0 {
		vm.releaseFrameValues()
		vm.popFrame()
	}
	if !threw && !vm.stop {
		vm.control = savedControl
		vm.line = savedLine
	}
	return ret, true, threw || vm.stop
}

func (vm *VM) openUserWrapperStream(z *zone.Zone, class, uri, mode string, context types.Value) (streams.WrapperStream, error) {
	if z == nil || vm.Zone != z {
		return nil, errors.New("invalid zone")
	}
	clean := strings.TrimPrefix(class, `\`)
	c, ok := vm.declaredClassByName(clean, true)
	if !ok || c == nil {
		return nil, errors.New(`class "` + class + `" not found`)
	}
	classID := c.ID
	var props uint32
	var hasDtor bool
	if c.PropsResolved {
		props = c.InstancePropCount
		hasDtor = c.HasDestructor
	} else {
		props = vm.instancePropCount(classID)
		hasDtor = vm.hasMethod(classID, "__destruct")
	}
	obj, err := types.NewObject(z, classID, props, hasDtor)
	if err != nil {
		return nil, err
	}
	vm.initProps(classID, obj)
	if vm.stop || vm.control {
		types.ReleaseValue(z, &obj)
		return nil, errors.New("property initialization failed")
	}

	ctxVal := types.NewNull()
	if !context.IsUndef() && !context.IsNull() {
		ctxVal = types.RetainValue(z, context)
	}
	if idx, pinfo, found := vm.propIndex(classID, "context", classID); found && !pinfo.Static {
		types.ObjectSet(z, obj, idx, ctxVal)
		types.ReleaseValue(z, &ctxVal)
	} else {
		_ = vm.dynamicPropSet(obj, "context", ctxVal)
		types.ReleaseValue(z, &ctxVal)
	}

	if m, _, hasCtor := vm.findMethod(classID, "__construct"); hasCtor && m.Func != nil {
		ret, _, failed := vm.invokeWrapperMethod(classID, obj, "__construct", nil)
		if !ret.IsUndef() {
			types.ReleaseValue(z, &ret)
		}
		if failed {
			types.ReleaseValue(z, &obj)
			return nil, errors.New("constructor failed")
		}
	}

	uriVal, err := types.NewString(z, uri, 0)
	if err != nil {
		types.ReleaseValue(z, &obj)
		return nil, err
	}
	modeVal, err := types.NewString(z, mode, 0)
	if err != nil {
		types.ReleaseValue(z, &uriVal)
		types.ReleaseValue(z, &obj)
		return nil, err
	}
	openedPathRef, err := types.NewReference(z, types.NewNull())
	if err != nil {
		types.ReleaseValue(z, &modeVal)
		types.ReleaseValue(z, &uriVal)
		types.ReleaseValue(z, &obj)
		return nil, err
	}
	args := []types.Value{uriVal, modeVal, types.NewLong(0), openedPathRef}
	ret, found, failed := vm.invokeWrapperMethod(classID, obj, "stream_open", args)
	if !found || failed {
		if !ret.IsUndef() {
			types.ReleaseValue(z, &ret)
		}
		types.ReleaseValue(z, &obj)
		if vm.effectiveErrorReporting()&eWarning == 0 {
			return nil, streams.ErrWrapperSilenced
		}
		return nil, nil
	}
	opened := types.ToBoolZ(z, ret)
	types.ReleaseValue(z, &ret)
	if !opened {
		types.ReleaseValue(z, &obj)
		if vm.effectiveErrorReporting()&eWarning == 0 {
			return nil, streams.ErrWrapperSilenced
		}
		return nil, nil
	}
	return &phpWrapperStream{
		vm:      vm,
		classID: classID,
		obj:     obj,
	}, nil
}

func (s *phpWrapperStream) Read(p []byte) (int, error) {
	if s.closed || len(p) == 0 {
		return 0, io.EOF
	}
	if len(s.buf) > 0 {
		n := copy(p, s.buf)
		s.buf = s.buf[n:]
		return n, nil
	}
	if s.eof {
		return 0, io.EOF
	}
	ret, found, failed := s.vm.invokeWrapperMethod(s.classID, s.obj, "stream_read", []types.Value{types.NewLong(int64(len(p)))})
	if !found || failed {
		if !ret.IsUndef() {
			types.ReleaseValue(s.vm.Zone, &ret)
		}
		s.eof = true
		return 0, io.EOF
	}
	d := types.Deref(s.vm.Zone, ret)
	if d.Type == types.TypeFalse || d.Type == types.TypeNull {
		types.ReleaseValue(s.vm.Zone, &ret)
		s.eof = true
		return 0, io.EOF
	}
	strVal, own := s.vm.stringify(d)
	var chunk []byte
	if strVal.Type == types.TypeString {
		raw := types.StringData(s.vm.Zone, strVal)
		if len(raw) > 0 {
			chunk = []byte(raw)
		}
	}
	if own {
		types.ReleaseValue(s.vm.Zone, &strVal)
	}
	types.ReleaseValue(s.vm.Zone, &ret)
	if len(chunk) == 0 {
		s.eof = true
		return 0, io.EOF
	}
	if eofRet, hasEOF, eofFailed := s.vm.invokeWrapperMethod(s.classID, s.obj, "stream_eof", nil); hasEOF {
		if !eofFailed {
			s.eof = types.ToBoolZ(s.vm.Zone, eofRet)
		}
		if !eofRet.IsUndef() {
			types.ReleaseValue(s.vm.Zone, &eofRet)
		}
	}
	n := copy(p, chunk)
	if n < len(chunk) {
		s.buf = append(s.buf[:0], chunk[n:]...)
	}
	return n, nil
}

func (s *phpWrapperStream) Write(p []byte) (int, error) {
	if s.closed {
		return 0, errors.New("stream closed")
	}
	if len(p) == 0 {
		return 0, nil
	}
	strVal, err := types.NewString(s.vm.Zone, string(p), 0)
	if err != nil {
		return 0, err
	}
	ret, found, failed := s.vm.invokeWrapperMethod(s.classID, s.obj, "stream_write", []types.Value{strVal})
	if !found || failed {
		if !ret.IsUndef() {
			types.ReleaseValue(s.vm.Zone, &ret)
		}
		return 0, errors.New("stream_write failed")
	}
	d := types.Deref(s.vm.Zone, ret)
	if d.Type == types.TypeFalse {
		types.ReleaseValue(s.vm.Zone, &ret)
		return 0, errors.New("stream_write returned false")
	}
	n := int(s.vm.toLong(d))
	types.ReleaseValue(s.vm.Zone, &ret)
	if n < 0 {
		n = 0
	}
	if n > len(p) {
		n = len(p)
	}
	return n, nil
}

func (s *phpWrapperStream) Seek(offset int64, whence int) (int64, error) {
	if s.closed {
		return 0, errors.New("stream closed")
	}
	var phpWhence int64
	switch whence {
	case io.SeekStart:
		phpWhence = streams.SEEK_SET
	case io.SeekCurrent:
		phpWhence = streams.SEEK_CUR
	case io.SeekEnd:
		phpWhence = streams.SEEK_END
	default:
		return 0, errors.New("invalid whence")
	}
	ret, found, failed := s.vm.invokeWrapperMethod(s.classID, s.obj, "stream_seek", []types.Value{types.NewLong(offset), types.NewLong(phpWhence)})
	if !found || failed {
		if !ret.IsUndef() {
			types.ReleaseValue(s.vm.Zone, &ret)
		}
		return 0, errors.New("stream_seek failed")
	}
	ok := types.ToBoolZ(s.vm.Zone, ret)
	types.ReleaseValue(s.vm.Zone, &ret)
	if !ok {
		return 0, errors.New("stream_seek refused")
	}
	s.buf = s.buf[:0]
	s.eof = false
	tellRet, hasTell, tellFailed := s.vm.invokeWrapperMethod(s.classID, s.obj, "stream_tell", nil)
	if !hasTell || tellFailed {
		if !tellRet.IsUndef() {
			types.ReleaseValue(s.vm.Zone, &tellRet)
		}
		if whence == io.SeekStart && offset >= 0 {
			return offset, nil
		}
		return 0, nil
	}
	pos := s.vm.toLong(types.Deref(s.vm.Zone, tellRet))
	types.ReleaseValue(s.vm.Zone, &tellRet)
	return pos, nil
}

func (s *phpWrapperStream) Flush() bool {
	if s.closed {
		return false
	}
	ret, found, failed := s.vm.invokeWrapperMethod(s.classID, s.obj, "stream_flush", nil)
	if !found {
		return false
	}
	if failed {
		if !ret.IsUndef() {
			types.ReleaseValue(s.vm.Zone, &ret)
		}
		return false
	}
	ok := types.ToBoolZ(s.vm.Zone, ret)
	types.ReleaseValue(s.vm.Zone, &ret)
	return ok
}

func (s *phpWrapperStream) EOF() bool {
	if s.closed || len(s.buf) > 0 {
		return s.closed
	}
	if s.eof {
		return true
	}
	ret, found, failed := s.vm.invokeWrapperMethod(s.classID, s.obj, "stream_eof", nil)
	if !found || failed {
		if !ret.IsUndef() {
			types.ReleaseValue(s.vm.Zone, &ret)
		}
		return s.eof
	}
	eof := types.ToBoolZ(s.vm.Zone, ret)
	types.ReleaseValue(s.vm.Zone, &ret)
	s.eof = eof
	return eof
}

func (s *phpWrapperStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.buf = nil
	if s.vm != nil && s.vm.Zone != nil && !s.obj.IsUndef() {
		ret, _, _ := s.vm.invokeWrapperMethod(s.classID, s.obj, "stream_close", nil)
		if !ret.IsUndef() {
			types.ReleaseValue(s.vm.Zone, &ret)
		}
		types.ReleaseValue(s.vm.Zone, &s.obj)
	}
	return nil
}
