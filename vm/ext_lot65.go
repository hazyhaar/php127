package vm

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/datetime"
	"code.hazyhaar.fr/devhoros/php127/reflection"
	"code.hazyhaar.fr/devhoros/php127/sapi"
	"code.hazyhaar.fr/devhoros/php127/session"
	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func (vm *VM) initSuperglobals() {
	sapiCtx := sapi.ActiveContext(vm.Zone)
	for _, name := range []string{"_GET", "_POST", "_COOKIE", "_REQUEST", "_SERVER", "_ENV", "_FILES"} {
		ref := vm.superglobalRef(name)
		if sapiCtx != nil {
			var src types.Value
			switch name {
			case "_GET":
				src = sapiCtx.Get
			case "_POST":
				src = sapiCtx.Post
			case "_COOKIE":
				src = sapiCtx.Cookie
			case "_REQUEST":
				src = sapiCtx.RequestVars
			case "_SERVER":
				src = sapiCtx.Server
			}
			if !src.IsUndef() && src.Type == types.TypeArray {
				types.AssignRef(vm.Zone, ref, src)
				continue
			}
		}
		d := types.Deref(vm.Zone, ref)
		if d.Type != types.TypeArray {
			arr, err := types.NewArray(vm.Zone)
			if err == nil {
				types.AssignRef(vm.Zone, ref, arr)
				types.ReleaseValue(vm.Zone, &arr)
			}
		}
	}
}

func (vm *VM) preNativeCallSync(name string) {
	switch strings.ToLower(strings.TrimPrefix(name, `\`)) {
	case "session_write_close", "session_commit", "session_encode":
		vm.syncSessionBeforeClose()
	}
}

func (vm *VM) postNativeCallSync(name string) {
	switch strings.ToLower(strings.TrimPrefix(name, `\`)) {
	case "session_start", "session_reset", "session_unset", "session_decode", "session_destroy":
		vm.sessionActive = true
		if dataPtr, err := session.Data(vm.Zone); err == nil && dataPtr != nil {
			ref := vm.superglobalRef("_SESSION")
			types.AssignRef(vm.Zone, ref, *dataPtr)
		}
	}
}

func (vm *VM) syncSessionBeforeClose() {
	if !vm.sessionActive {
		return
	}
	if session.Status(vm.Zone) != session.PHP_SESSION_ACTIVE {
		return
	}
	v, ok := vm.globals["_SESSION"]
	if !ok || v.IsUndef() {
		return
	}
	d := types.Deref(vm.Zone, v)
	if d.Type != types.TypeArray {
		return
	}
	dataPtr, err := session.Data(vm.Zone)
	if err != nil || dataPtr == nil {
		return
	}
	if dataPtr.Type == types.TypeArray && dataPtr.DataSlot == d.DataSlot {
		return
	}
	retained := types.RetainValue(vm.Zone, d)
	if !dataPtr.IsUndef() {
		types.ReleaseValue(vm.Zone, dataPtr)
	}
	*dataPtr = retained
}

func (vm *VM) doDateTimeNew(classID uint32, spec callSpec, line uint32) bool {
	defer vm.releaseArgs(spec.args)
	cname, _ := bytecode.BuiltinClassName(classID)
	switch classID {
	case bytecode.ClassDateTime, bytecode.ClassDateTimeImmutable:
		if len(spec.args) > 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::__construct() expects at most 2 arguments, %d given", cname, len(spec.args)), 0)
			return true
		}
		dtStr := "now"
		if len(spec.args) >= 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
			s, own := vm.stringify(spec.args[0])
			dtStr = strings.Clone(types.StringData(vm.Zone, s))
			if own {
				types.ReleaseValue(vm.Zone, &s)
			}
		}
		var tz *datetime.DateTimeZone
		if len(spec.args) >= 2 && !types.Deref(vm.Zone, spec.args[1]).IsNull() {
			var err error
			tz, err = datetime.TimeZoneFromObject(vm.Zone, spec.args[1])
			if err != nil {
				vm.raiseNativeError(err, cname+"::__construct", spec.args, line)
				return true
			}
		}
		var obj types.Value
		var err error
		if classID == bytecode.ClassDateTimeImmutable {
			obj, _, err = datetime.NewDateTimeImmutable(vm.Zone, dtStr, tz)
		} else {
			obj, _, err = datetime.NewDateTime(vm.Zone, dtStr, tz)
		}
		if err != nil {
			vm.raiseNativeError(err, cname+"::__construct", spec.args, line)
			return true
		}
		vm.push(obj)
		vm.bump()
		return true

	case bytecode.ClassDateTimeZone:
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTimeZone::__construct() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		s, own := vm.stringify(spec.args[0])
		tzStr := strings.Clone(types.StringData(vm.Zone, s))
		if own {
			types.ReleaseValue(vm.Zone, &s)
		}
		obj, _, err := datetime.NewDateTimeZone(vm.Zone, tzStr)
		if err != nil {
			vm.raiseNativeError(err, "DateTimeZone::__construct", spec.args, line)
			return true
		}
		vm.push(obj)
		vm.bump()
		return true

	case bytecode.ClassDateInterval:
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateInterval::__construct() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		s, own := vm.stringify(spec.args[0])
		ivStr := strings.Clone(types.StringData(vm.Zone, s))
		if own {
			types.ReleaseValue(vm.Zone, &s)
		}
		obj, _, err := datetime.NewDateInterval(vm.Zone, ivStr)
		if err != nil {
			vm.raiseNativeError(err, "DateInterval::__construct", spec.args, line)
			return true
		}
		vm.push(obj)
		vm.bump()
		return true

	case bytecode.ClassDatePeriod:
		if len(spec.args) < 3 || len(spec.args) > 4 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DatePeriod::__construct() expects 3 or 4 arguments, %d given", len(spec.args)), 0)
			return true
		}
		start, err := datetime.DateTimeInterfaceFromObject(vm.Zone, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "DatePeriod::__construct", spec.args, line)
			return true
		}
		iv, err := datetime.IntervalFromObject(vm.Zone, spec.args[1])
		if err != nil {
			vm.raiseNativeError(err, "DatePeriod::__construct", spec.args, line)
			return true
		}
		opts := int64(0)
		if len(spec.args) == 4 {
			opts = vm.toLong(spec.args[3])
		}
		arg2 := types.Deref(vm.Zone, spec.args[2])
		var obj types.Value
		if arg2.Type == types.TypeObject {
			end, eerr := datetime.DateTimeInterfaceFromObject(vm.Zone, arg2)
			if eerr != nil {
				vm.raiseNativeError(eerr, "DatePeriod::__construct", spec.args, line)
				return true
			}
			obj, _, err = datetime.NewDatePeriodWithEnd(vm.Zone, start, iv, end, opts)
		} else {
			rec := int(vm.toLong(arg2))
			obj, _, err = datetime.NewDatePeriodWithRecurrences(vm.Zone, start, iv, rec, opts)
		}
		if err != nil {
			vm.raiseNativeError(err, "DatePeriod::__construct", spec.args, line)
			return true
		}
		vm.push(obj)
		vm.bump()
		return true
	}
	return false
}

func (vm *VM) callDateTimeStatic(classID uint32, spec callSpec, line uint32) bool {
	defer vm.releaseArgs(spec.args)
	cname, _ := bytecode.BuiltinClassName(classID)
	method := strings.ToLower(spec.method)
	switch classID {
	case bytecode.ClassDateTime:
		switch method {
		case "createfromformat":
			if len(spec.args) < 2 || len(spec.args) > 3 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTime::createFromFormat() expects 2 or 3 arguments, %d given", len(spec.args)), 0)
				return true
			}
			fStr := vm.argCloneString(spec.args[0])
			dStr := vm.argCloneString(spec.args[1])
			var tz *datetime.DateTimeZone
			if len(spec.args) == 3 && !types.Deref(vm.Zone, spec.args[2]).IsNull() {
				var err error
				tz, err = datetime.TimeZoneFromObject(vm.Zone, spec.args[2])
				if err != nil {
					vm.raiseNativeError(err, "DateTime::createFromFormat", spec.args, line)
					return true
				}
			}
			obj, _, err := datetime.CreateFromFormat(vm.Zone, fStr, dStr, tz)
			if err != nil {
				vm.push(types.NewBool(false))
				vm.bump()
				return true
			}
			vm.push(obj)
			vm.bump()
			return true
		case "createfromimmutable":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTime::createFromImmutable() expects exactly 1 argument, %d given", len(spec.args)), 0)
				return true
			}
			imm, err := datetime.DateTimeImmutableFromObject(vm.Zone, spec.args[0])
			if err != nil {
				vm.raiseNativeError(err, "DateTime::createFromImmutable", spec.args, line)
				return true
			}
			obj, _, err := datetime.CreateFromImmutable(vm.Zone, imm)
			if err != nil {
				vm.raiseNativeError(err, "DateTime::createFromImmutable", spec.args, line)
				return true
			}
			vm.push(obj)
			vm.bump()
			return true
		case "createfrominterface":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTime::createFromInterface() expects exactly 1 argument, %d given", len(spec.args)), 0)
				return true
			}
			dti, err := datetime.DateTimeInterfaceFromObject(vm.Zone, spec.args[0])
			if err != nil {
				vm.raiseNativeError(err, "DateTime::createFromInterface", spec.args, line)
				return true
			}
			obj, _, err := datetime.CreateFromInterfaceMutable(vm.Zone, dti)
			if err != nil {
				vm.raiseNativeError(err, "DateTime::createFromInterface", spec.args, line)
				return true
			}
			vm.push(obj)
			vm.bump()
			return true
		}
	case bytecode.ClassDateTimeImmutable:
		switch method {
		case "createfromformat":
			if len(spec.args) < 2 || len(spec.args) > 3 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTimeImmutable::createFromFormat() expects 2 or 3 arguments, %d given", len(spec.args)), 0)
				return true
			}
			fStr := vm.argCloneString(spec.args[0])
			dStr := vm.argCloneString(spec.args[1])
			var tz *datetime.DateTimeZone
			if len(spec.args) == 3 && !types.Deref(vm.Zone, spec.args[2]).IsNull() {
				var err error
				tz, err = datetime.TimeZoneFromObject(vm.Zone, spec.args[2])
				if err != nil {
					vm.raiseNativeError(err, "DateTimeImmutable::createFromFormat", spec.args, line)
					return true
				}
			}
			obj, _, err := datetime.CreateImmutableFromFormat(vm.Zone, fStr, dStr, tz)
			if err != nil {
				vm.push(types.NewBool(false))
				vm.bump()
				return true
			}
			vm.push(obj)
			vm.bump()
			return true
		case "createfrommutable":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTimeImmutable::createFromMutable() expects exactly 1 argument, %d given", len(spec.args)), 0)
				return true
			}
			mut, err := datetime.DateTimeFromObject(vm.Zone, spec.args[0])
			if err != nil {
				vm.raiseNativeError(err, "DateTimeImmutable::createFromMutable", spec.args, line)
				return true
			}
			obj, _, err := datetime.CreateFromMutable(vm.Zone, mut)
			if err != nil {
				vm.raiseNativeError(err, "DateTimeImmutable::createFromMutable", spec.args, line)
				return true
			}
			vm.push(obj)
			vm.bump()
			return true
		case "createfrominterface":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTimeImmutable::createFromInterface() expects exactly 1 argument, %d given", len(spec.args)), 0)
				return true
			}
			dti, err := datetime.DateTimeInterfaceFromObject(vm.Zone, spec.args[0])
			if err != nil {
				vm.raiseNativeError(err, "DateTimeImmutable::createFromInterface", spec.args, line)
				return true
			}
			obj, _, err := datetime.CreateFromInterfaceImmutable(vm.Zone, dti)
			if err != nil {
				vm.raiseNativeError(err, "DateTimeImmutable::createFromInterface", spec.args, line)
				return true
			}
			vm.push(obj)
			vm.bump()
			return true
		}
	case bytecode.ClassDateTimeZone:
		if method == "listidentifiers" {
			arr, err := datetime.ListIdentifiers(vm.Zone)
			if err != nil {
				vm.raiseNativeError(err, "DateTimeZone::listIdentifiers", spec.args, line)
				return true
			}
			vm.push(arr)
			vm.bump()
			return true
		}
	case bytecode.ClassDateInterval:
		if method == "createfromdatestring" {
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateInterval::createFromDateString() expects exactly 1 argument, %d given", len(spec.args)), 0)
				return true
			}
			rel := vm.argCloneString(spec.args[0])
			obj, _, err := datetime.DateIntervalCreateFromDateString(vm.Zone, rel)
			if err != nil {
				vm.raiseNativeError(err, "DateInterval::createFromDateString", spec.args, line)
				return true
			}
			vm.push(obj)
			vm.bump()
			return true
		}
	}
	vm.raiseBuiltin(bytecode.ClassError, "Call to undefined method "+cname+"::"+spec.method+"()", 0)
	return true
}

func (vm *VM) callDateTimeInstance(obj types.Value, classID uint32, spec callSpec, line uint32) {
	defer vm.releaseArgs(spec.args)
	defer vm.releaseSpecObj(spec)
	cname, _ := bytecode.BuiltinClassName(classID)
	method := strings.ToLower(spec.method)

	switch classID {
	case bytecode.ClassDateTime, bytecode.ClassDateTimeImmutable:
		dti, err := datetime.DateTimeInterfaceFromObject(vm.Zone, obj)
		if err != nil {
			vm.raiseNativeError(err, cname+"::"+spec.method, spec.args, line)
			return
		}
		switch method {
		case "format":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::format() expects exactly 1 argument, %d given", cname, len(spec.args)), 0)
				return
			}
			s := dti.Format(vm.argCloneString(spec.args[0]))
			sv, err := types.NewString(vm.Zone, s, 0)
			if err != nil {
				vm.raiseNativeError(err, cname+"::format", spec.args, line)
				return
			}
			vm.push(sv)
			vm.bump()
			return
		case "gettimestamp":
			vm.push(types.NewLong(dti.GetTimestamp()))
			vm.bump()
			return
		case "getoffset":
			vm.push(types.NewLong(dti.GetOffset()))
			vm.bump()
			return
		case "gettimezone":
			tzObj, _, err := dti.GetTimezone(vm.Zone)
			if err != nil {
				vm.push(types.NewBool(false))
				vm.bump()
				return
			}
			vm.push(tzObj)
			vm.bump()
			return
		case "diff":
			if len(spec.args) < 1 || len(spec.args) > 2 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("%s::diff() expects 1 or 2 arguments, %d given", cname, len(spec.args)), 0)
				return
			}
			target, err := datetime.DateTimeInterfaceFromObject(vm.Zone, spec.args[0])
			if err != nil {
				vm.raiseNativeError(err, cname+"::diff", spec.args, line)
				return
			}
			abs := false
			if len(spec.args) == 2 {
				abs = types.ToBoolZ(vm.Zone, spec.args[1])
			}
			ivObj, _, err := dti.Diff(vm.Zone, target, abs)
			if err != nil {
				vm.raiseNativeError(err, cname+"::diff", spec.args, line)
				return
			}
			vm.push(ivObj)
			vm.bump()
			return
		}

		if classID == bytecode.ClassDateTime {
			dt, _ := datetime.DateTimeFromObject(vm.Zone, obj)
			switch method {
			case "modify":
				if len(spec.args) != 1 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTime::modify() expects exactly 1 argument, %d given", len(spec.args)), 0)
					return
				}
				if _, err := dt.Modify(vm.argCloneString(spec.args[0])); err != nil {
					vm.raiseNativeError(err, "DateTime::modify", spec.args, line)
					return
				}
				vm.push(types.RetainValue(vm.Zone, obj))
				vm.bump()
				return
			case "add":
				if len(spec.args) != 1 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTime::add() expects exactly 1 argument, %d given", len(spec.args)), 0)
					return
				}
				iv, err := datetime.IntervalFromObject(vm.Zone, spec.args[0])
				if err != nil {
					vm.raiseNativeError(err, "DateTime::add", spec.args, line)
					return
				}
				_, _ = dt.Add(iv)
				vm.push(types.RetainValue(vm.Zone, obj))
				vm.bump()
				return
			case "sub":
				if len(spec.args) != 1 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTime::sub() expects exactly 1 argument, %d given", len(spec.args)), 0)
					return
				}
				iv, err := datetime.IntervalFromObject(vm.Zone, spec.args[0])
				if err != nil {
					vm.raiseNativeError(err, "DateTime::sub", spec.args, line)
					return
				}
				_, _ = dt.Sub(iv)
				vm.push(types.RetainValue(vm.Zone, obj))
				vm.bump()
				return
			case "settimezone":
				if len(spec.args) != 1 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTime::setTimezone() expects exactly 1 argument, %d given", len(spec.args)), 0)
					return
				}
				tz, err := datetime.TimeZoneFromObject(vm.Zone, spec.args[0])
				if err != nil {
					vm.raiseNativeError(err, "DateTime::setTimezone", spec.args, line)
					return
				}
				_, _ = dt.SetTimezone(tz)
				vm.push(types.RetainValue(vm.Zone, obj))
				vm.bump()
				return
			case "settimestamp":
				if len(spec.args) != 1 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTime::setTimestamp() expects exactly 1 argument, %d given", len(spec.args)), 0)
					return
				}
				dt.SetTimestamp(vm.toLong(spec.args[0]))
				vm.push(types.RetainValue(vm.Zone, obj))
				vm.bump()
				return
			case "setdate":
				if len(spec.args) != 3 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTime::setDate() expects exactly 3 arguments, %d given", len(spec.args)), 0)
					return
				}
				dt.SetDate(int(vm.toLong(spec.args[0])), int(vm.toLong(spec.args[1])), int(vm.toLong(spec.args[2])))
				vm.push(types.RetainValue(vm.Zone, obj))
				vm.bump()
				return
			case "settime":
				if len(spec.args) < 2 || len(spec.args) > 4 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTime::setTime() expects between 2 and 4 arguments, %d given", len(spec.args)), 0)
					return
				}
				sec, us := 0, 0
				if len(spec.args) >= 3 {
					sec = int(vm.toLong(spec.args[2]))
				}
				if len(spec.args) == 4 {
					us = int(vm.toLong(spec.args[3]))
				}
				dt.SetTime(int(vm.toLong(spec.args[0])), int(vm.toLong(spec.args[1])), sec, us)
				vm.push(types.RetainValue(vm.Zone, obj))
				vm.bump()
				return
			case "setisodate":
				if len(spec.args) < 2 || len(spec.args) > 3 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTime::setISODate() expects 2 or 3 arguments, %d given", len(spec.args)), 0)
					return
				}
				dow := 1
				if len(spec.args) == 3 {
					dow = int(vm.toLong(spec.args[2]))
				}
				dt.SetISODate(int(vm.toLong(spec.args[0])), int(vm.toLong(spec.args[1])), dow)
				vm.push(types.RetainValue(vm.Zone, obj))
				vm.bump()
				return
			}
		} else {
			dtiImm, _ := datetime.DateTimeImmutableFromObject(vm.Zone, obj)
			switch method {
			case "modify":
				if len(spec.args) != 1 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTimeImmutable::modify() expects exactly 1 argument, %d given", len(spec.args)), 0)
					return
				}
				nextObj, _, err := dtiImm.Modify(vm.Zone, vm.argCloneString(spec.args[0]))
				if err != nil {
					vm.raiseNativeError(err, "DateTimeImmutable::modify", spec.args, line)
					return
				}
				vm.push(nextObj)
				vm.bump()
				return
			case "add":
				if len(spec.args) != 1 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTimeImmutable::add() expects exactly 1 argument, %d given", len(spec.args)), 0)
					return
				}
				iv, err := datetime.IntervalFromObject(vm.Zone, spec.args[0])
				if err != nil {
					vm.raiseNativeError(err, "DateTimeImmutable::add", spec.args, line)
					return
				}
				nextObj, _, err := dtiImm.Add(vm.Zone, iv)
				if err != nil {
					vm.raiseNativeError(err, "DateTimeImmutable::add", spec.args, line)
					return
				}
				vm.push(nextObj)
				vm.bump()
				return
			case "sub":
				if len(spec.args) != 1 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTimeImmutable::sub() expects exactly 1 argument, %d given", len(spec.args)), 0)
					return
				}
				iv, err := datetime.IntervalFromObject(vm.Zone, spec.args[0])
				if err != nil {
					vm.raiseNativeError(err, "DateTimeImmutable::sub", spec.args, line)
					return
				}
				nextObj, _, err := dtiImm.Sub(vm.Zone, iv)
				if err != nil {
					vm.raiseNativeError(err, "DateTimeImmutable::sub", spec.args, line)
					return
				}
				vm.push(nextObj)
				vm.bump()
				return
			case "settimezone":
				if len(spec.args) != 1 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTimeImmutable::setTimezone() expects exactly 1 argument, %d given", len(spec.args)), 0)
					return
				}
				tz, err := datetime.TimeZoneFromObject(vm.Zone, spec.args[0])
				if err != nil {
					vm.raiseNativeError(err, "DateTimeImmutable::setTimezone", spec.args, line)
					return
				}
				nextObj, _, err := dtiImm.SetTimezone(vm.Zone, tz)
				if err != nil {
					vm.raiseNativeError(err, "DateTimeImmutable::setTimezone", spec.args, line)
					return
				}
				vm.push(nextObj)
				vm.bump()
				return
			case "settimestamp":
				if len(spec.args) != 1 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTimeImmutable::setTimestamp() expects exactly 1 argument, %d given", len(spec.args)), 0)
					return
				}
				nextObj, _, err := dtiImm.SetTimestamp(vm.Zone, vm.toLong(spec.args[0]))
				if err != nil {
					vm.raiseNativeError(err, "DateTimeImmutable::setTimestamp", spec.args, line)
					return
				}
				vm.push(nextObj)
				vm.bump()
				return
			case "setdate":
				if len(spec.args) != 3 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTimeImmutable::setDate() expects exactly 3 arguments, %d given", len(spec.args)), 0)
					return
				}
				nextObj, _, err := dtiImm.SetDate(vm.Zone, int(vm.toLong(spec.args[0])), int(vm.toLong(spec.args[1])), int(vm.toLong(spec.args[2])))
				if err != nil {
					vm.raiseNativeError(err, "DateTimeImmutable::setDate", spec.args, line)
					return
				}
				vm.push(nextObj)
				vm.bump()
				return
			case "settime":
				if len(spec.args) < 2 || len(spec.args) > 4 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTimeImmutable::setTime() expects between 2 and 4 arguments, %d given", len(spec.args)), 0)
					return
				}
				sec, us := 0, 0
				if len(spec.args) >= 3 {
					sec = int(vm.toLong(spec.args[2]))
				}
				if len(spec.args) == 4 {
					us = int(vm.toLong(spec.args[3]))
				}
				nextObj, _, err := dtiImm.SetTime(vm.Zone, int(vm.toLong(spec.args[0])), int(vm.toLong(spec.args[1])), sec, us)
				if err != nil {
					vm.raiseNativeError(err, "DateTimeImmutable::setTime", spec.args, line)
					return
				}
				vm.push(nextObj)
				vm.bump()
				return
			case "setisodate":
				if len(spec.args) < 2 || len(spec.args) > 3 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTimeImmutable::setISODate() expects 2 or 3 arguments, %d given", len(spec.args)), 0)
					return
				}
				dow := 1
				if len(spec.args) == 3 {
					dow = int(vm.toLong(spec.args[2]))
				}
				nextObj, _, err := dtiImm.SetISODate(vm.Zone, int(vm.toLong(spec.args[0])), int(vm.toLong(spec.args[1])), dow)
				if err != nil {
					vm.raiseNativeError(err, "DateTimeImmutable::setISODate", spec.args, line)
					return
				}
				vm.push(nextObj)
				vm.bump()
				return
			}
		}

	case bytecode.ClassDateTimeZone:
		tz, err := datetime.TimeZoneFromObject(vm.Zone, obj)
		if err != nil {
			vm.raiseNativeError(err, "DateTimeZone::"+spec.method, spec.args, line)
			return
		}
		switch method {
		case "getname":
			sv, _ := types.NewString(vm.Zone, tz.GetName(), 0)
			vm.push(sv)
			vm.bump()
			return
		case "getoffset":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateTimeZone::getOffset() expects exactly 1 argument, %d given", len(spec.args)), 0)
				return
			}
			dti, err := datetime.DateTimeInterfaceFromObject(vm.Zone, spec.args[0])
			if err != nil {
				vm.raiseNativeError(err, "DateTimeZone::getOffset", spec.args, line)
				return
			}
			vm.push(types.NewLong(tz.GetOffset(dti)))
			vm.bump()
			return
		case "listidentifiers":
			arr, err := datetime.ListIdentifiers(vm.Zone)
			if err != nil {
				vm.raiseNativeError(err, "DateTimeZone::listIdentifiers", spec.args, line)
				return
			}
			vm.push(arr)
			vm.bump()
			return
		}

	case bytecode.ClassDateInterval:
		iv, err := datetime.IntervalFromObject(vm.Zone, obj)
		if err != nil {
			vm.raiseNativeError(err, "DateInterval::"+spec.method, spec.args, line)
			return
		}
		if method == "format" {
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("DateInterval::format() expects exactly 1 argument, %d given", len(spec.args)), 0)
				return
			}
			sv, _ := types.NewString(vm.Zone, iv.Format(vm.argCloneString(spec.args[0])), 0)
			vm.push(sv)
			vm.bump()
			return
		}

	case bytecode.ClassDatePeriod:
		p, err := datetime.PeriodFromObject(vm.Zone, obj)
		if err != nil {
			vm.raiseNativeError(err, "DatePeriod::"+spec.method, spec.args, line)
			return
		}
		switch method {
		case "getstartdate":
			vm.push(types.RetainValue(vm.Zone, p.GetStartDate().Object()))
			vm.bump()
			return
		case "getenddate":
			if p.GetEndDate() == nil {
				vm.push(types.NewNull())
			} else {
				vm.push(types.RetainValue(vm.Zone, p.GetEndDate().Object()))
			}
			vm.bump()
			return
		case "getdateinterval":
			vm.push(types.RetainValue(vm.Zone, p.GetDateInterval().Object()))
			vm.bump()
			return
		case "getrecurrences":
			if p.GetEndDate() != nil {
				vm.push(types.NewNull())
			} else {
				vm.push(types.NewLong(int64(p.GetRecurrences())))
			}
			vm.bump()
			return
		}
	}

	vm.raiseBuiltin(bytecode.ClassError, "Call to undefined method "+cname+"::"+spec.method+"()", 0)
}

func (vm *VM) argCloneString(v types.Value) string {
	s, own := vm.stringify(v)
	res := strings.Clone(types.StringData(vm.Zone, s))
	if own {
		types.ReleaseValue(vm.Zone, &s)
	}
	return res
}

type vmReflectionProvider struct {
	vm *VM
}

func (p *vmReflectionProvider) ResolveClass(name string) (*reflection.ClassMeta, bool) {
	if id, ok := p.vm.resolveName(strings.TrimPrefix(name, `\`)); ok {
		return p.ResolveClassByID(id)
	}
	return nil, false
}

func (p *vmReflectionProvider) ResolveClassByID(id uint32) (*reflection.ClassMeta, bool) {
	c := p.vm.classByID(id)
	if c == nil {
		return nil, false
	}
	return p.vm.buildClassMeta(c), true
}

func (p *vmReflectionProvider) ResolveFunction(name string) (*reflection.FunctionMeta, bool) {
	clean := strings.TrimPrefix(name, `\`)
	if p.vm.Program != nil {
		for _, fn := range p.vm.Program.Funcs {
			if fn != nil && strings.EqualFold(fn.Name, clean) {
				return p.vm.buildFunctionMeta(fn), true
			}
		}
	}
	if native, ok := lookupNative(clean); ok {
		return &reflection.FunctionMeta{
			Name:       clean,
			IsInternal: true,
			Invoker: func(z *zone.Zone, args []types.Value) (types.Value, error) {
				return native(z, p.vm.Out, args)
			},
		}, true
	}
	return nil, false
}

func convertVis(v bytecode.Visibility) reflection.Visibility {
	switch v {
	case bytecode.VisProtected:
		return reflection.VisProtected
	case bytecode.VisPrivate:
		return reflection.VisPrivate
	default:
		return reflection.VisPublic
	}
}

func (vm *VM) convertAttrs(attrs []bytecode.AttributeProto) []reflection.AttributeMeta {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]reflection.AttributeMeta, len(attrs))
	for i, a := range attrs {
		am := reflection.AttributeMeta{Name: a.Name}
		for _, arg := range a.Args {
			var v types.Value
			if arg.Value != nil {
				v = vm.constantValue(arg.Value)
			} else {
				v = types.NewNull()
			}
			am.Args = append(am.Args, reflection.AttributeArg{Name: arg.Name, Value: v})
		}
		out[i] = am
	}
	return out
}

func (vm *VM) convertParams(params []bytecode.ParamInfo) []reflection.ParamMeta {
	if len(params) == 0 {
		return nil
	}
	out := make([]reflection.ParamMeta, len(params))
	for i, p := range params {
		pm := reflection.ParamMeta{
			Name:          p.Name,
			Position:      i,
			Type:          reflection.ParseTypeString(p.TypeName),
			ByRef:         p.ByRef,
			Variadic:      p.Variadic,
			Promoted:      p.Promoted,
			HasDefault:    p.HasDefault,
			Default:       p.Default,
			DefaultString: p.DefaultString,
			Attributes:    vm.convertAttrs(p.Attributes),
		}
		out[i] = pm
	}
	return out
}

func (vm *VM) buildFunctionMeta(fn *bytecode.OpArray) *reflection.FunctionMeta {
	return &reflection.FunctionMeta{
		Name:        fn.Name,
		IsInternal:  false,
		IsClosure:   fn.Name == "{closure}",
		IsGenerator: fn.IsGenerator,
		ByRef:       fn.ReturnByRef,
		ReturnType:  reflection.ParseTypeString(fn.ReturnType),
		Params:      vm.convertParams(fn.Params),
		Attributes:  vm.convertAttrs(fn.Attributes),
		Invoker: func(z *zone.Zone, args []types.Value) (types.Value, error) {
			inArgs := make([]types.Value, len(args))
			for i, a := range args {
				inArgs[i] = types.RetainValue(z, a)
			}
			ret, threw := vm.invokeOpArray(fn, types.Value{}, false, inArgs, 0, 0, vm.line)
			if threw || vm.hasThrow {
				return types.NewNull(), reflection.ReflectionException{Message: "Exception during function invocation"}
			}
			vm.control = false
			return ret, nil
		},
	}
}

func (vm *VM) buildClassMeta(c *bytecode.ClassProto) *reflection.ClassMeta {
	parentName := c.ParentName
	if parentName == "" && c.ParentID != 0 {
		parentName = vm.className(c.ParentID)
	}
	cm := &reflection.ClassMeta{
		Name:        c.Name,
		ID:          c.ID,
		ParentName:  parentName,
		Interfaces:  c.Interfaces,
		Traits:      c.Traits,
		IsInternal:  c.ID < bytecode.ClassUser || c.ID >= 5000,
		IsInterface: c.Kind == bytecode.KindInterface,
		IsTrait:     c.Kind == bytecode.KindTrait,
		IsEnum:      c.Kind == bytecode.KindEnum,
		IsAbstract:  c.Flags&bytecode.ClassAbstract != 0,
		IsFinal:     c.Flags&bytecode.ClassFinal != 0,
		IsReadOnly:  c.Flags&bytecode.ClassReadonly != 0,
		BackingType: reflection.ParseTypeString(c.BackingType),
		Attributes:  vm.convertAttrs(c.Attributes),
	}

	// Collect properties with exact slot indices
	var chain []*bytecode.ClassProto
	for cur := c; cur != nil; cur = vm.classByID(cur.ParentID) {
		chain = append(chain, cur)
		if cur.ParentID == 0 || cur.ParentID == cur.ID {
			break
		}
	}
	slotIdx := uint32(0)
	if vm.isExceptionFamily(c.ID) && c.ID >= bytecode.ClassUser {
		slotIdx = 4
	}
	slotByClassAndProp := make(map[string]uint32)
	inheritedNonPrivate := make(map[string]uint32)
	for _, cl := range slices.Backward(chain) {
		for _, p := range cl.Props {
			if !p.Static {
				var idx uint32
				if prevIdx, ok := inheritedNonPrivate[p.Name]; ok {
					idx = prevIdx
				} else {
					idx = slotIdx
					slotIdx++
				}
				slotByClassAndProp[fmt.Sprintf("%d:%s", cl.ID, p.Name)] = idx
				if p.Visibility != bytecode.VisPrivate {
					inheritedNonPrivate[p.Name] = idx
				} else {
					delete(inheritedNonPrivate, p.Name)
				}
			}
		}
	}
	for _, p := range c.Props {
		idx := slotByClassAndProp[fmt.Sprintf("%d:%s", c.ID, p.Name)]
		pm := reflection.PropertyMeta{
			Name:           p.Name,
			DeclaringClass: c.Name,
			Index:          idx,
			Visibility:     convertVis(p.Visibility),
			IsStatic:       p.Static,
			IsReadOnly:     p.Readonly,
			IsDefault:      true,
			IsPromoted:     p.Promoted,
			Type:           reflection.ParseTypeString(p.TypeName),
			HasDefault:     p.HasDefault,
			Default:        p.Default,
			Attributes:     vm.convertAttrs(p.Attributes),
		}
		if p.Initial != nil {
			pm.HasDefault = true
			if p.Initial.IsString {
				pm.DefaultString = p.Initial.Text
			} else {
				pm.Default = p.Initial.Value
			}
		}
		cm.Properties = append(cm.Properties, pm)
	}

	for _, m := range c.Methods {
		mCopy := m
		var params []reflection.ParamMeta
		var retType *reflection.TypeMeta
		var isGen, byRef bool
		if mCopy.Func != nil {
			params = vm.convertParams(mCopy.Func.Params)
			retType = reflection.ParseTypeString(mCopy.Func.ReturnType)
			isGen = mCopy.Func.IsGenerator
			byRef = mCopy.Func.ReturnByRef
		}
		declClass := c.Name
		if mCopy.ClassID != 0 && mCopy.ClassID != c.ID {
			declClass = vm.className(mCopy.ClassID)
		}
		mm := reflection.MethodMeta{
			Name:           mCopy.Name,
			DeclaringClass: declClass,
			Visibility:     convertVis(mCopy.Visibility),
			IsStatic:       mCopy.Static,
			IsAbstract:     mCopy.Abstract,
			IsFinal:        mCopy.Final,
			IsInternal:     cm.IsInternal,
			IsGenerator:    isGen,
			ByRef:          byRef,
			ReturnType:     retType,
			Params:         params,
			Attributes:     vm.convertAttrs(mCopy.Attributes),
		}
		if mCopy.Func != nil {
			mm.Invoker = func(z *zone.Zone, recv types.Value, args []types.Value) (types.Value, error) {
				inArgs := make([]types.Value, len(args))
				for i, a := range args {
					inArgs[i] = types.RetainValue(z, a)
				}
				hasThis := !mCopy.Static && recv.Type == types.TypeObject
				var thisVal types.Value
				if hasThis {
					thisVal = types.RetainValue(z, recv)
				}
				ret, threw := vm.invokeOpArray(mCopy.Func, thisVal, hasThis, inArgs, c.ID, mCopy.ClassID, vm.line)
				if threw || vm.hasThrow {
					return types.NewNull(), reflection.ReflectionException{Message: "Exception during method invocation"}
				}
				vm.control = false
				return ret, nil
			}
		}
		cm.Methods = append(cm.Methods, mm)
	}

	for _, cst := range c.Constants {
		cmeta := reflection.ConstantMeta{
			Name:           cst.Name,
			DeclaringClass: c.Name,
			Visibility:     convertVis(cst.Visibility),
			IsFinal:        cst.Final,
			Value:          cst.Value,
			ValueString:    cst.Text,
			HasString:      cst.IsString,
			Attributes:     vm.convertAttrs(cst.Attributes),
		}
		if cst.Initial != nil {
			if cst.Initial.IsString {
				cmeta.HasString = true
				cmeta.ValueString = cst.Initial.Text
			} else {
				cmeta.Value = vm.constantValue(cst.Initial)
			}
		}
		cm.Constants = append(cm.Constants, cmeta)
	}

	if c.Kind == bytecode.KindEnum {
		cases := vm.enumCases(c)
		for i, ec := range c.Cases {
			ecMeta := reflection.EnumCaseMeta{
				Name:       ec.Name,
				EnumClass:  c.Name,
				IsBacked:   c.BackingType != "",
				UnitObject: cases[i],
				Attributes: vm.convertAttrs(ec.Attributes),
			}
			if ec.Value != nil {
				if ec.Value.IsString {
					ecMeta.HasString = true
					ecMeta.BackingString = ec.Value.Text
				} else {
					ecMeta.BackingValue = ec.Value.Value
				}
			}
			cm.EnumCases = append(cm.EnumCases, ecMeta)
			cm.Constants = append(cm.Constants, reflection.ConstantMeta{
				Name:           ec.Name,
				DeclaringClass: c.Name,
				Visibility:     reflection.VisPublic,
				IsFinal:        true,
				Value:          cases[i],
				Attributes:     ecMeta.Attributes,
			})
		}
	}

	cm.PropGetter = func(z *zone.Zone, class *reflection.ClassMeta, prop *reflection.PropertyMeta, obj types.Value) (types.Value, bool, error) {
		if prop.IsStatic {
			idx, ok := vm.staticIndex(class.ID, prop.Name)
			if !ok {
				return types.NewNull(), false, nil
			}
			slots := vm.staticSlots(class.ID)
			if int(idx) >= len(slots) || slots[idx].IsUndef() {
				return types.NewNull(), false, nil
			}
			return types.RetainValue(z, types.Deref(z, slots[idx])), true, nil
		}
		v := types.ObjectGet(z, obj, prop.Index)
		if v.IsUndef() {
			return types.NewNull(), false, nil
		}
		return types.RetainValue(z, types.Deref(z, v)), true, nil
	}
	cm.PropSetter = func(z *zone.Zone, class *reflection.ClassMeta, prop *reflection.PropertyMeta, obj types.Value, val types.Value) error {
		if prop.IsStatic {
			idx, ok := vm.staticIndex(class.ID, prop.Name)
			if !ok {
				return nil
			}
			slots := vm.staticSlots(class.ID)
			if int(idx) < len(slots) {
				types.ReleaseValue(z, &slots[idx])
				slots[idx] = types.RetainValue(z, types.Deref(z, val))
			}
			return nil
		}
		types.ObjectSet(z, obj, prop.Index, types.Deref(z, val))
		return nil
	}
	cm.Instantiator = func(z *zone.Zone, class *reflection.ClassMeta, withConstructor bool, args []types.Value) (types.Value, error) {
		obj, err := types.NewObject(z, class.ID, vm.instancePropCount(class.ID), vm.hasMethod(class.ID, "__destruct"))
		if err != nil {
			return types.Value{}, err
		}
		vm.initProps(class.ID, obj)
		if withConstructor {
			if m, _, ok := vm.findMethod(class.ID, "__construct"); ok && m.Func != nil {
				inArgs := make([]types.Value, len(args))
				for i, a := range args {
					inArgs[i] = types.RetainValue(z, a)
				}
				ret, threw := vm.invokeOpArray(m.Func, types.RetainValue(z, obj), true, inArgs, class.ID, m.ClassID, vm.line)
				if !threw {
					types.ReleaseValue(z, &ret)
				}
				if threw || vm.hasThrow {
					types.ReleaseObject(z, &obj)
					return types.Value{}, reflection.ReflectionException{Message: "Constructor threw an exception"}
				}
				vm.control = false
			}
		}
		return obj, nil
	}

	return cm
}

func (vm *VM) doReflectionNew(classID uint32, spec callSpec, line uint32) bool {
	defer vm.releaseArgs(spec.args)
	cname, _ := bytecode.BuiltinClassName(classID)
	p := reflection.ActiveProvider(vm.Zone)
	if p == nil && vm.Zone != nil {
		vm.reflProvider.vm = vm
		reflection.BindProvider(vm.Zone, &vm.reflProvider)
		p = &vm.reflProvider
	}

	switch classID {
	case bytecode.ClassReflectionClass:
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ReflectionClass::__construct() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		rc, err := reflection.NewReflectionClass(vm.Zone, p, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "ReflectionClass::__construct", spec.args, line)
			return true
		}
		vm.push(rc.Object())
		vm.bump()
		return true

	case bytecode.ClassReflectionObject:
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ReflectionObject::__construct() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		ro, err := reflection.NewReflectionObject(vm.Zone, p, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "ReflectionObject::__construct", spec.args, line)
			return true
		}
		vm.push(ro.Object())
		vm.bump()
		return true

	case bytecode.ClassReflectionFunction:
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ReflectionFunction::__construct() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		arg0 := types.Deref(vm.Zone, spec.args[0])
		var fnTarget any = arg0
		if arg0.Type == types.TypeObject && types.ObjectInfo(vm.Zone, arg0).ClassID == bytecode.ClassClosure {
			idx := int(types.ObjectGet(vm.Zone, arg0, 0).ToLong())
			if idx >= 0 && idx < len(vm.closures) {
				fnTarget = vm.buildFunctionMeta(vm.closures[idx].fn)
			}
		}
		rf, err := reflection.NewReflectionFunction(vm.Zone, p, fnTarget)
		if err != nil {
			vm.raiseNativeError(err, "ReflectionFunction::__construct", spec.args, line)
			return true
		}
		vm.push(rf.Object())
		vm.bump()
		return true

	case bytecode.ClassReflectionMethod:
		if len(spec.args) < 1 || len(spec.args) > 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ReflectionMethod::__construct() expects 1 or 2 arguments, %d given", len(spec.args)), 0)
			return true
		}
		var rm *reflection.ReflectionMethod
		var err error
		if len(spec.args) == 1 {
			rm, err = reflection.NewReflectionMethod(vm.Zone, p, vm.argCloneString(spec.args[0]))
		} else {
			rm, err = reflection.NewReflectionMethod(vm.Zone, p, spec.args[0], vm.argCloneString(spec.args[1]))
		}
		if err != nil {
			vm.raiseNativeError(err, "ReflectionMethod::__construct", spec.args, line)
			return true
		}
		vm.push(rm.Object())
		vm.bump()
		return true

	case bytecode.ClassReflectionProperty:
		if len(spec.args) != 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ReflectionProperty::__construct() expects exactly 2 arguments, %d given", len(spec.args)), 0)
			return true
		}
		rp, err := reflection.NewReflectionProperty(vm.Zone, p, spec.args[0], vm.argCloneString(spec.args[1]))
		if err != nil {
			vm.raiseNativeError(err, "ReflectionProperty::__construct", spec.args, line)
			return true
		}
		vm.push(rp.Object())
		vm.bump()
		return true

	case bytecode.ClassReflectionParameter:
		if len(spec.args) != 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ReflectionParameter::__construct() expects exactly 2 arguments, %d given", len(spec.args)), 0)
			return true
		}
		fnVal := types.Deref(vm.Zone, spec.args[0])
		var fnSpec any
		switch fnVal.Type {
		case types.TypeString:
			fnSpec = vm.argCloneString(fnVal)
		case types.TypeArray:
			v0, _, _ := types.ArrayGet(vm.Zone, fnVal, types.NewLong(0), nil)
			v1, _, _ := types.ArrayGet(vm.Zone, fnVal, types.NewLong(1), nil)
			fnSpec = [2]string{vm.argCloneString(v0), vm.argCloneString(v1)}
		}
		pvVal := types.Deref(vm.Zone, spec.args[1])
		var paramSpec any
		if pvVal.Type == types.TypeLong {
			paramSpec = int(pvVal.ToLong())
		} else {
			paramSpec = vm.argCloneString(pvVal)
		}
		rparam, err := reflection.NewReflectionParameter(vm.Zone, p, fnSpec, paramSpec)
		if err != nil {
			vm.raiseNativeError(err, "ReflectionParameter::__construct", spec.args, line)
			return true
		}
		vm.push(rparam.Object())
		vm.bump()
		return true

	case bytecode.ClassReflectionClassConstant:
		if len(spec.args) != 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ReflectionClassConstant::__construct() expects exactly 2 arguments, %d given", len(spec.args)), 0)
			return true
		}
		rcc, err := reflection.NewReflectionClassConstant(vm.Zone, p, spec.args[0], vm.argCloneString(spec.args[1]))
		if err != nil {
			vm.raiseNativeError(err, "ReflectionClassConstant::__construct", spec.args, line)
			return true
		}
		vm.push(rcc.Object())
		vm.bump()
		return true

	case bytecode.ClassReflectionEnum:
		if len(spec.args) != 1 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ReflectionEnum::__construct() expects exactly 1 argument, %d given", len(spec.args)), 0)
			return true
		}
		re, err := reflection.NewReflectionEnum(vm.Zone, p, spec.args[0])
		if err != nil {
			vm.raiseNativeError(err, "ReflectionEnum::__construct", spec.args, line)
			return true
		}
		vm.push(re.Object())
		vm.bump()
		return true

	case bytecode.ClassReflectionEnumUnitCase:
		if len(spec.args) != 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ReflectionEnumUnitCase::__construct() expects exactly 2 arguments, %d given", len(spec.args)), 0)
			return true
		}
		uc, err := reflection.NewReflectionEnumUnitCase(vm.Zone, p, spec.args[0], vm.argCloneString(spec.args[1]))
		if err != nil {
			vm.raiseNativeError(err, "ReflectionEnumUnitCase::__construct", spec.args, line)
			return true
		}
		vm.push(uc.Object())
		vm.bump()
		return true

	case bytecode.ClassReflectionEnumBackedCase:
		if len(spec.args) != 2 {
			vm.raiseBuiltin(bytecode.ClassArgumentCountError, fmt.Sprintf("ReflectionEnumBackedCase::__construct() expects exactly 2 arguments, %d given", len(spec.args)), 0)
			return true
		}
		bc, err := reflection.NewReflectionEnumBackedCase(vm.Zone, p, spec.args[0], vm.argCloneString(spec.args[1]))
		if err != nil {
			vm.raiseNativeError(err, "ReflectionEnumBackedCase::__construct", spec.args, line)
			return true
		}
		vm.push(bc.Object())
		vm.bump()
		return true
	}
	vm.raiseBuiltin(bytecode.ClassError, "Cannot instantiate "+cname, 0)
	return true
}

func (vm *VM) pushReflString(s string) {
	sv, _ := types.NewString(vm.Zone, s, 0)
	vm.push(sv)
	vm.bump()
}

func (vm *VM) pushReflObjects(objs []types.Value) {
	arr, _ := types.NewArray(vm.Zone)
	for _, o := range objs {
		_ = types.ArrayAppend(vm.Zone, &arr, types.RetainValue(vm.Zone, o))
	}
	vm.push(arr)
	vm.bump()
}

func (vm *VM) callReflectionInstance(obj types.Value, classID uint32, spec callSpec, line uint32) {
	defer vm.releaseArgs(spec.args)
	defer vm.releaseSpecObj(spec)
	cname, _ := bytecode.BuiltinClassName(classID)
	method := strings.ToLower(spec.method)

	switch classID {
	case bytecode.ClassReflectionClass, bytecode.ClassReflectionObject, bytecode.ClassReflectionEnum:
		rc, ok := reflection.ClassFromObject(vm.Zone, obj)
		if !ok {
			vm.raiseBuiltin(bytecode.ClassError, "Invalid ReflectionClass object", 0)
			return
		}
		if classID == bytecode.ClassReflectionEnum {
			re := &reflection.ReflectionEnum{ReflectionClass: rc}
			switch method {
			case "isbacked":
				vm.push(types.NewBool(re.IsBacked()))
				vm.bump()
				return
			case "getbackingtype":
				rt, err := re.GetBackingType()
				if err != nil {
					vm.raiseNativeError(err, cname+"::getBackingType", spec.args, line)
					return
				}
				if rt == nil {
					vm.push(types.NewNull())
				} else {
					vm.push(rt.Object())
				}
				vm.bump()
				return
			case "hascase":
				if len(spec.args) != 1 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, "ReflectionEnum::hasCase() expects exactly 1 argument", 0)
					return
				}
				vm.push(types.NewBool(re.HasCase(vm.argCloneString(spec.args[0]))))
				vm.bump()
				return
			case "getcase":
				if len(spec.args) != 1 {
					vm.raiseBuiltin(bytecode.ClassArgumentCountError, "ReflectionEnum::getCase() expects exactly 1 argument", 0)
					return
				}
				uc, err := re.GetCase(vm.argCloneString(spec.args[0]))
				if err != nil {
					vm.raiseNativeError(err, "ReflectionEnum::getCase", spec.args, line)
					return
				}
				vm.push(uc.Object())
				vm.bump()
				return
			case "getcases":
				cases, err := re.GetCases()
				if err != nil {
					vm.raiseNativeError(err, "ReflectionEnum::getCases", spec.args, line)
					return
				}
				objs := make([]types.Value, len(cases))
				for i, c := range cases {
					objs[i] = c.Object()
				}
				vm.pushReflObjects(objs)
				return
			}
		}
		switch method {
		case "getname":
			vm.pushReflString(rc.GetName())
			return
		case "getshortname":
			vm.pushReflString(rc.GetShortName())
			return
		case "getnamespacename":
			vm.pushReflString(rc.GetNamespaceName())
			return
		case "innamespace":
			vm.push(types.NewBool(rc.InNamespace()))
			vm.bump()
			return
		case "isinternal":
			vm.push(types.NewBool(rc.IsInternal()))
			vm.bump()
			return
		case "isuserdefined":
			vm.push(types.NewBool(rc.IsUserDefined()))
			vm.bump()
			return
		case "isinterface":
			vm.push(types.NewBool(rc.IsInterface()))
			vm.bump()
			return
		case "istrait":
			vm.push(types.NewBool(rc.IsTrait()))
			vm.bump()
			return
		case "isenum":
			vm.push(types.NewBool(rc.IsEnum()))
			vm.bump()
			return
		case "isabstract":
			vm.push(types.NewBool(rc.IsAbstract()))
			vm.bump()
			return
		case "isfinal":
			vm.push(types.NewBool(rc.IsFinal()))
			vm.bump()
			return
		case "isreadonly":
			vm.push(types.NewBool(rc.IsReadOnly()))
			vm.bump()
			return
		case "isanonymous":
			vm.push(types.NewBool(rc.IsAnonymous()))
			vm.bump()
			return
		case "isinstantiable":
			vm.push(types.NewBool(rc.IsInstantiable()))
			vm.bump()
			return
		case "iscloneable":
			vm.push(types.NewBool(rc.IsCloneable()))
			vm.bump()
			return
		case "getmodifiers":
			vm.push(types.NewLong(rc.GetModifiers()))
			vm.bump()
			return
		case "getparentclass":
			parent, err := rc.GetParentClass()
			if err != nil {
				vm.raiseNativeError(err, cname+"::getParentClass", spec.args, line)
				return
			}
			if parent == nil {
				vm.push(types.NewBool(false))
			} else {
				vm.push(parent.Object())
			}
			vm.bump()
			return
		case "issubclassof":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, cname+"::isSubclassOf() expects exactly 1 argument", 0)
				return
			}
			b, err := rc.IsSubclassOf(spec.args[0])
			if err != nil {
				vm.raiseNativeError(err, cname+"::isSubclassOf", spec.args, line)
				return
			}
			vm.push(types.NewBool(b))
			vm.bump()
			return
		case "implementsinterface":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, cname+"::implementsInterface() expects exactly 1 argument", 0)
				return
			}
			b, err := rc.ImplementsInterface(spec.args[0])
			if err != nil {
				vm.raiseNativeError(err, cname+"::implementsInterface", spec.args, line)
				return
			}
			vm.push(types.NewBool(b))
			vm.bump()
			return
		case "getinterfacenames":
			names := rc.GetInterfaceNames()
			arr, _ := types.NewArray(vm.Zone)
			for _, n := range names {
				sv, _ := types.NewString(vm.Zone, n, 0)
				_ = types.ArrayAppend(vm.Zone, &arr, sv)
				types.ReleaseValue(vm.Zone, &sv)
			}
			vm.push(arr)
			vm.bump()
			return
		case "gettraitnames":
			names := rc.GetTraitNames()
			arr, _ := types.NewArray(vm.Zone)
			for _, n := range names {
				sv, _ := types.NewString(vm.Zone, n, 0)
				_ = types.ArrayAppend(vm.Zone, &arr, sv)
				types.ReleaseValue(vm.Zone, &sv)
			}
			vm.push(arr)
			vm.bump()
			return
		case "hasmethod":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, cname+"::hasMethod() expects exactly 1 argument", 0)
				return
			}
			vm.push(types.NewBool(rc.HasMethod(vm.argCloneString(spec.args[0]))))
			vm.bump()
			return
		case "getmethod":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, cname+"::getMethod() expects exactly 1 argument", 0)
				return
			}
			rm, err := rc.GetMethod(vm.argCloneString(spec.args[0]))
			if err != nil {
				vm.raiseNativeError(err, cname+"::getMethod", spec.args, line)
				return
			}
			vm.push(rm.Object())
			vm.bump()
			return
		case "getmethods":
			var filter []int64
			if len(spec.args) >= 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
				filter = []int64{vm.toLong(spec.args[0])}
			}
			methods, err := rc.GetMethods(filter...)
			if err != nil {
				vm.raiseNativeError(err, cname+"::getMethods", spec.args, line)
				return
			}
			objs := make([]types.Value, len(methods))
			for i, m := range methods {
				objs[i] = m.Object()
			}
			vm.pushReflObjects(objs)
			return
		case "getconstructor":
			rm, err := rc.GetConstructor()
			if err != nil {
				vm.raiseNativeError(err, cname+"::getConstructor", spec.args, line)
				return
			}
			if rm == nil {
				vm.push(types.NewNull())
			} else {
				vm.push(rm.Object())
			}
			vm.bump()
			return
		case "hasproperty":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, cname+"::hasProperty() expects exactly 1 argument", 0)
				return
			}
			vm.push(types.NewBool(rc.HasProperty(vm.argCloneString(spec.args[0]))))
			vm.bump()
			return
		case "getproperty":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, cname+"::getProperty() expects exactly 1 argument", 0)
				return
			}
			rp, err := rc.GetProperty(vm.argCloneString(spec.args[0]))
			if err != nil {
				vm.raiseNativeError(err, cname+"::getProperty", spec.args, line)
				return
			}
			vm.push(rp.Object())
			vm.bump()
			return
		case "getproperties":
			var filter []int64
			if len(spec.args) >= 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
				filter = []int64{vm.toLong(spec.args[0])}
			}
			props, err := rc.GetProperties(filter...)
			if err != nil {
				vm.raiseNativeError(err, cname+"::getProperties", spec.args, line)
				return
			}
			objs := make([]types.Value, len(props))
			for i, p := range props {
				objs[i] = p.Object()
			}
			vm.pushReflObjects(objs)
			return
		case "hasconstant":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, cname+"::hasConstant() expects exactly 1 argument", 0)
				return
			}
			vm.push(types.NewBool(rc.HasConstant(vm.argCloneString(spec.args[0]))))
			vm.bump()
			return
		case "getconstant":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, cname+"::getConstant() expects exactly 1 argument", 0)
				return
			}
			val, _, err := rc.GetConstant(vm.argCloneString(spec.args[0]))
			if err != nil {
				vm.raiseNativeError(err, cname+"::getConstant", spec.args, line)
				return
			}
			vm.push(types.RetainValue(vm.Zone, val))
			vm.bump()
			return
		case "getconstants":
			var filter []int64
			if len(spec.args) >= 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
				filter = []int64{vm.toLong(spec.args[0])}
			}
			arr, err := rc.GetConstants(filter...)
			if err != nil {
				vm.raiseNativeError(err, cname+"::getConstants", spec.args, line)
				return
			}
			vm.push(arr)
			vm.bump()
			return
		case "getreflectionconstant":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, cname+"::getReflectionConstant() expects exactly 1 argument", 0)
				return
			}
			rcc, err := rc.GetReflectionConstant(vm.argCloneString(spec.args[0]))
			if err != nil {
				vm.raiseNativeError(err, cname+"::getReflectionConstant", spec.args, line)
				return
			}
			if rcc == nil {
				vm.push(types.NewBool(false))
			} else {
				vm.push(rcc.Object())
			}
			vm.bump()
			return
		case "getreflectionconstants":
			var filter []int64
			if len(spec.args) >= 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
				filter = []int64{vm.toLong(spec.args[0])}
			}
			rccs, err := rc.GetReflectionConstants(filter...)
			if err != nil {
				vm.raiseNativeError(err, cname+"::getReflectionConstants", spec.args, line)
				return
			}
			objs := make([]types.Value, len(rccs))
			for i, rcc := range rccs {
				objs[i] = rcc.Object()
			}
			vm.pushReflObjects(objs)
			return
		case "newinstance":
			inst, err := rc.NewInstance(spec.args...)
			if err != nil {
				vm.raiseNativeError(err, cname+"::newInstance", spec.args, line)
				return
			}
			vm.push(inst)
			vm.bump()
			return
		case "newinstanceargs":
			var argsSlice []types.Value
			if len(spec.args) >= 1 {
				arr := types.Deref(vm.Zone, spec.args[0])
				if arr.Type == types.TypeArray {
					types.ArrayForEach(vm.Zone, arr, func(_, v types.Value) bool {
						argsSlice = append(argsSlice, types.Deref(vm.Zone, v))
						return true
					})
				}
			}
			inst, err := rc.NewInstanceArgs(argsSlice)
			if err != nil {
				vm.raiseNativeError(err, cname+"::newInstanceArgs", spec.args, line)
				return
			}
			vm.push(inst)
			vm.bump()
			return
		case "newinstancewithoutconstructor":
			inst, err := rc.NewInstanceWithoutConstructor()
			if err != nil {
				vm.raiseNativeError(err, cname+"::newInstanceWithoutConstructor", spec.args, line)
				return
			}
			vm.push(inst)
			vm.bump()
			return
		case "isinstance":
			if len(spec.args) != 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, cname+"::isInstance() expects exactly 1 argument", 0)
				return
			}
			b, err := rc.IsInstance(spec.args[0])
			if err != nil {
				vm.raiseNativeError(err, cname+"::isInstance", spec.args, line)
				return
			}
			vm.push(types.NewBool(b))
			vm.bump()
			return
		case "getattributes":
			fName := ""
			fFlags := int64(0)
			if len(spec.args) >= 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
				fName = vm.argCloneString(spec.args[0])
			}
			if len(spec.args) >= 2 {
				fFlags = vm.toLong(spec.args[1])
			}
			attrs := rc.GetAttributes(fName, fFlags)
			objs := make([]types.Value, len(attrs))
			for i, a := range attrs {
				objs[i] = a.Object()
			}
			vm.pushReflObjects(objs)
			return
		case "__tostring":
			vm.pushReflString(rc.String())
			return
		}

	case bytecode.ClassReflectionMethod:
		rm, ok := reflection.MethodFromObject(vm.Zone, obj)
		if !ok {
			vm.raiseBuiltin(bytecode.ClassError, "Invalid ReflectionMethod object", 0)
			return
		}
		switch method {
		case "getname":
			vm.pushReflString(rm.GetName())
			return
		case "getshortname":
			vm.pushReflString(rm.GetShortName())
			return
		case "getnamespacename":
			vm.pushReflString(rm.GetNamespaceName())
			return
		case "innamespace":
			vm.push(types.NewBool(rm.InNamespace()))
			vm.bump()
			return
		case "ispublic":
			vm.push(types.NewBool(rm.IsPublic()))
			vm.bump()
			return
		case "isprotected":
			vm.push(types.NewBool(rm.IsProtected()))
			vm.bump()
			return
		case "isprivate":
			vm.push(types.NewBool(rm.IsPrivate()))
			vm.bump()
			return
		case "isstatic":
			vm.push(types.NewBool(rm.IsStatic()))
			vm.bump()
			return
		case "isabstract":
			vm.push(types.NewBool(rm.IsAbstract()))
			vm.bump()
			return
		case "isfinal":
			vm.push(types.NewBool(rm.IsFinal()))
			vm.bump()
			return
		case "isinternal":
			vm.push(types.NewBool(rm.IsInternal()))
			vm.bump()
			return
		case "isuserdefined":
			vm.push(types.NewBool(rm.IsUserDefined()))
			vm.bump()
			return
		case "isgenerator":
			vm.push(types.NewBool(rm.IsGenerator()))
			vm.bump()
			return
		case "isclosure":
			vm.push(types.NewBool(rm.IsClosure()))
			vm.bump()
			return
		case "returnsreference":
			vm.push(types.NewBool(rm.ReturnsReference()))
			vm.bump()
			return
		case "isconstructor":
			vm.push(types.NewBool(rm.IsConstructor()))
			vm.bump()
			return
		case "isdestructor":
			vm.push(types.NewBool(rm.IsDestructor()))
			vm.bump()
			return
		case "isvariadic":
			vm.push(types.NewBool(rm.IsVariadic()))
			vm.bump()
			return
		case "getmodifiers":
			vm.push(types.NewLong(rm.GetModifiers()))
			vm.bump()
			return
		case "getnumberofparameters":
			vm.push(types.NewLong(int64(rm.GetNumberOfParameters())))
			vm.bump()
			return
		case "getnumberofrequiredparameters":
			vm.push(types.NewLong(int64(rm.GetNumberOfRequiredParameters())))
			vm.bump()
			return
		case "getparameters":
			params, err := rm.GetParameters()
			if err != nil {
				vm.raiseNativeError(err, "ReflectionMethod::getParameters", spec.args, line)
				return
			}
			objs := make([]types.Value, len(params))
			for i, p := range params {
				objs[i] = p.Object()
			}
			vm.pushReflObjects(objs)
			return
		case "hasreturntype":
			vm.push(types.NewBool(rm.HasReturnType()))
			vm.bump()
			return
		case "getreturntype":
			rt, err := rm.GetReturnType()
			if err != nil {
				vm.raiseNativeError(err, "ReflectionMethod::getReturnType", spec.args, line)
				return
			}
			if rt == nil {
				vm.push(types.NewNull())
			} else {
				vm.push(rt.Object())
			}
			vm.bump()
			return
		case "getdeclaringclass":
			dc, err := rm.GetDeclaringClass()
			if err != nil {
				vm.raiseNativeError(err, "ReflectionMethod::getDeclaringClass", spec.args, line)
				return
			}
			vm.push(dc.Object())
			vm.bump()
			return
		case "setaccessible":
			vm.push(types.NewNull())
			vm.bump()
			return
		case "invoke":
			if len(spec.args) < 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, "ReflectionMethod::invoke() expects at least 1 argument", 0)
				return
			}
			ret, err := rm.Invoke(spec.args[0], spec.args[1:]...)
			if err != nil {
				vm.raiseNativeError(err, "ReflectionMethod::invoke", spec.args, line)
				return
			}
			vm.push(types.RetainValue(vm.Zone, ret))
			vm.bump()
			return
		case "invokeargs":
			if len(spec.args) != 2 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, "ReflectionMethod::invokeArgs() expects exactly 2 arguments", 0)
				return
			}
			var argsSlice []types.Value
			arr := types.Deref(vm.Zone, spec.args[1])
			if arr.Type == types.TypeArray {
				types.ArrayForEach(vm.Zone, arr, func(_, v types.Value) bool {
					argsSlice = append(argsSlice, types.Deref(vm.Zone, v))
					return true
				})
			}
			ret, err := rm.InvokeArgs(spec.args[0], argsSlice)
			if err != nil {
				vm.raiseNativeError(err, "ReflectionMethod::invokeArgs", spec.args, line)
				return
			}
			vm.push(types.RetainValue(vm.Zone, ret))
			vm.bump()
			return
		case "getattributes":
			fName := ""
			fFlags := int64(0)
			if len(spec.args) >= 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
				fName = vm.argCloneString(spec.args[0])
			}
			if len(spec.args) >= 2 {
				fFlags = vm.toLong(spec.args[1])
			}
			attrs := rm.GetAttributes(fName, fFlags)
			objs := make([]types.Value, len(attrs))
			for i, a := range attrs {
				objs[i] = a.Object()
			}
			vm.pushReflObjects(objs)
			return
		case "__tostring":
			vm.pushReflString(rm.String())
			return
		}

	case bytecode.ClassReflectionFunction:
		rf, ok := reflection.FunctionFromObject(vm.Zone, obj)
		if !ok {
			vm.raiseBuiltin(bytecode.ClassError, "Invalid ReflectionFunction object", 0)
			return
		}
		switch method {
		case "getname":
			vm.pushReflString(rf.GetName())
			return
		case "getshortname":
			vm.pushReflString(rf.GetShortName())
			return
		case "getnamespacename":
			vm.pushReflString(rf.GetNamespaceName())
			return
		case "innamespace":
			vm.push(types.NewBool(rf.InNamespace()))
			vm.bump()
			return
		case "isinternal":
			vm.push(types.NewBool(rf.IsInternal()))
			vm.bump()
			return
		case "isuserdefined":
			vm.push(types.NewBool(rf.IsUserDefined()))
			vm.bump()
			return
		case "isclosure":
			vm.push(types.NewBool(rf.IsClosure()))
			vm.bump()
			return
		case "isgenerator":
			vm.push(types.NewBool(rf.IsGenerator()))
			vm.bump()
			return
		case "returnsreference":
			vm.push(types.NewBool(rf.ReturnsReference()))
			vm.bump()
			return
		case "isvariadic":
			vm.push(types.NewBool(rf.IsVariadic()))
			vm.bump()
			return
		case "getnumberofparameters":
			vm.push(types.NewLong(int64(rf.GetNumberOfParameters())))
			vm.bump()
			return
		case "getnumberofrequiredparameters":
			vm.push(types.NewLong(int64(rf.GetNumberOfRequiredParameters())))
			vm.bump()
			return
		case "getparameters":
			params, err := rf.GetParameters()
			if err != nil {
				vm.raiseNativeError(err, "ReflectionFunction::getParameters", spec.args, line)
				return
			}
			objs := make([]types.Value, len(params))
			for i, p := range params {
				objs[i] = p.Object()
			}
			vm.pushReflObjects(objs)
			return
		case "hasreturntype":
			vm.push(types.NewBool(rf.HasReturnType()))
			vm.bump()
			return
		case "getreturntype":
			rt, err := rf.GetReturnType()
			if err != nil {
				vm.raiseNativeError(err, "ReflectionFunction::getReturnType", spec.args, line)
				return
			}
			if rt == nil {
				vm.push(types.NewNull())
			} else {
				vm.push(rt.Object())
			}
			vm.bump()
			return
		case "invoke":
			ret, err := rf.Invoke(spec.args...)
			if err != nil {
				vm.raiseNativeError(err, "ReflectionFunction::invoke", spec.args, line)
				return
			}
			vm.push(types.RetainValue(vm.Zone, ret))
			vm.bump()
			return
		case "invokeargs":
			var argsSlice []types.Value
			if len(spec.args) >= 1 {
				arr := types.Deref(vm.Zone, spec.args[0])
				if arr.Type == types.TypeArray {
					types.ArrayForEach(vm.Zone, arr, func(_, v types.Value) bool {
						argsSlice = append(argsSlice, types.Deref(vm.Zone, v))
						return true
					})
				}
			}
			ret, err := rf.InvokeArgs(argsSlice)
			if err != nil {
				vm.raiseNativeError(err, "ReflectionFunction::invokeArgs", spec.args, line)
				return
			}
			vm.push(types.RetainValue(vm.Zone, ret))
			vm.bump()
			return
		case "getattributes":
			fName := ""
			fFlags := int64(0)
			if len(spec.args) >= 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
				fName = vm.argCloneString(spec.args[0])
			}
			if len(spec.args) >= 2 {
				fFlags = vm.toLong(spec.args[1])
			}
			attrs := rf.GetAttributes(fName, fFlags)
			objs := make([]types.Value, len(attrs))
			for i, a := range attrs {
				objs[i] = a.Object()
			}
			vm.pushReflObjects(objs)
			return
		case "__tostring":
			vm.pushReflString(rf.String())
			return
		}

	case bytecode.ClassReflectionProperty:
		rp, ok := reflection.PropertyFromObject(vm.Zone, obj)
		if !ok {
			vm.raiseBuiltin(bytecode.ClassError, "Invalid ReflectionProperty object", 0)
			return
		}
		switch method {
		case "getname":
			vm.pushReflString(rp.GetName())
			return
		case "ispublic":
			vm.push(types.NewBool(rp.IsPublic()))
			vm.bump()
			return
		case "isprotected":
			vm.push(types.NewBool(rp.IsProtected()))
			vm.bump()
			return
		case "isprivate":
			vm.push(types.NewBool(rp.IsPrivate()))
			vm.bump()
			return
		case "isstatic":
			vm.push(types.NewBool(rp.IsStatic()))
			vm.bump()
			return
		case "isreadonly":
			vm.push(types.NewBool(rp.IsReadOnly()))
			vm.bump()
			return
		case "isdefault":
			vm.push(types.NewBool(rp.IsDefault()))
			vm.bump()
			return
		case "ispromoted":
			vm.push(types.NewBool(rp.IsPromoted()))
			vm.bump()
			return
		case "getmodifiers":
			vm.push(types.NewLong(rp.GetModifiers()))
			vm.bump()
			return
		case "hastype":
			vm.push(types.NewBool(rp.HasType()))
			vm.bump()
			return
		case "gettype":
			rt, err := rp.GetType()
			if err != nil {
				vm.raiseNativeError(err, "ReflectionProperty::getType", spec.args, line)
				return
			}
			if rt == nil {
				vm.push(types.NewNull())
			} else {
				vm.push(rt.Object())
			}
			vm.bump()
			return
		case "hasdefaultvalue":
			vm.push(types.NewBool(rp.HasDefaultValue()))
			vm.bump()
			return
		case "getdefaultvalue":
			val, err := rp.GetDefaultValue()
			if err != nil {
				vm.raiseNativeError(err, "ReflectionProperty::getDefaultValue", spec.args, line)
				return
			}
			vm.push(types.RetainValue(vm.Zone, val))
			vm.bump()
			return
		case "setaccessible":
			vm.push(types.NewNull())
			vm.bump()
			return
		case "isinitialized":
			b, err := rp.IsInitialized(spec.args...)
			if err != nil {
				vm.raiseNativeError(err, "ReflectionProperty::isInitialized", spec.args, line)
				return
			}
			vm.push(types.NewBool(b))
			vm.bump()
			return
		case "getvalue":
			val, err := rp.GetValue(spec.args...)
			if err != nil {
				vm.raiseNativeError(err, "ReflectionProperty::getValue", spec.args, line)
				return
			}
			vm.push(types.RetainValue(vm.Zone, val))
			vm.bump()
			return
		case "setvalue":
			if len(spec.args) < 1 {
				vm.raiseBuiltin(bytecode.ClassArgumentCountError, "ReflectionProperty::setValue() expects at least 1 argument", 0)
				return
			}
			if err := rp.SetValue(spec.args[0], spec.args[1:]...); err != nil {
				vm.raiseNativeError(err, "ReflectionProperty::setValue", spec.args, line)
				return
			}
			vm.push(types.NewNull())
			vm.bump()
			return
		case "getdeclaringclass":
			dc, err := rp.GetDeclaringClass()
			if err != nil {
				vm.raiseNativeError(err, "ReflectionProperty::getDeclaringClass", spec.args, line)
				return
			}
			vm.push(dc.Object())
			vm.bump()
			return
		case "getattributes":
			fName := ""
			fFlags := int64(0)
			if len(spec.args) >= 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
				fName = vm.argCloneString(spec.args[0])
			}
			if len(spec.args) >= 2 {
				fFlags = vm.toLong(spec.args[1])
			}
			attrs := rp.GetAttributes(fName, fFlags)
			objs := make([]types.Value, len(attrs))
			for i, a := range attrs {
				objs[i] = a.Object()
			}
			vm.pushReflObjects(objs)
			return
		case "__tostring":
			vm.pushReflString(rp.String())
			return
		}

	case bytecode.ClassReflectionParameter:
		rp, ok := reflection.ParameterFromObject(vm.Zone, obj)
		if !ok {
			vm.raiseBuiltin(bytecode.ClassError, "Invalid ReflectionParameter object", 0)
			return
		}
		switch method {
		case "getname":
			vm.pushReflString(rp.GetName())
			return
		case "getposition":
			vm.push(types.NewLong(int64(rp.GetPosition())))
			vm.bump()
			return
		case "hastype":
			vm.push(types.NewBool(rp.HasType()))
			vm.bump()
			return
		case "gettype":
			rt, err := rp.GetType()
			if err != nil {
				vm.raiseNativeError(err, "ReflectionParameter::getType", spec.args, line)
				return
			}
			if rt == nil {
				vm.push(types.NewNull())
			} else {
				vm.push(rt.Object())
			}
			vm.bump()
			return
		case "ispassedbyreference":
			vm.push(types.NewBool(rp.IsPassedByReference()))
			vm.bump()
			return
		case "isvariadic":
			vm.push(types.NewBool(rp.IsVariadic()))
			vm.bump()
			return
		case "ispromoted":
			vm.push(types.NewBool(rp.IsPromoted()))
			vm.bump()
			return
		case "allowsnull":
			vm.push(types.NewBool(rp.AllowsNull()))
			vm.bump()
			return
		case "isoptional":
			vm.push(types.NewBool(rp.IsOptional()))
			vm.bump()
			return
		case "isdefaultvalueavailable":
			vm.push(types.NewBool(rp.IsDefaultValueAvailable()))
			vm.bump()
			return
		case "getdefaultvalue":
			val, err := rp.GetDefaultValue()
			if err != nil {
				vm.raiseNativeError(err, "ReflectionParameter::getDefaultValue", spec.args, line)
				return
			}
			vm.push(types.RetainValue(vm.Zone, val))
			vm.bump()
			return
		case "getdeclaringclass":
			dc, err := rp.GetDeclaringClass()
			if err != nil {
				vm.raiseNativeError(err, "ReflectionParameter::getDeclaringClass", spec.args, line)
				return
			}
			if dc == nil {
				vm.push(types.NewNull())
			} else {
				vm.push(dc.Object())
			}
			vm.bump()
			return
		case "getattributes":
			fName := ""
			fFlags := int64(0)
			if len(spec.args) >= 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
				fName = vm.argCloneString(spec.args[0])
			}
			if len(spec.args) >= 2 {
				fFlags = vm.toLong(spec.args[1])
			}
			attrs := rp.GetAttributes(fName, fFlags)
			objs := make([]types.Value, len(attrs))
			for i, a := range attrs {
				objs[i] = a.Object()
			}
			vm.pushReflObjects(objs)
			return
		case "__tostring":
			vm.pushReflString(rp.String())
			return
		}

	case bytecode.ClassReflectionClassConstant, bytecode.ClassReflectionEnumUnitCase, bytecode.ClassReflectionEnumBackedCase:
		if classID == bytecode.ClassReflectionEnumUnitCase || classID == bytecode.ClassReflectionEnumBackedCase {
			if uc, ok := reflection.EnumUnitCaseFromObject(vm.Zone, obj); ok {
				switch method {
				case "getenum":
					re, err := uc.GetEnum()
					if err != nil {
						vm.raiseNativeError(err, cname+"::getEnum", spec.args, line)
						return
					}
					vm.push(re.Object())
					vm.bump()
					return
				case "getbackingvalue":
					bv, err := uc.GetBackingValue()
					if err != nil {
						vm.raiseNativeError(err, cname+"::getBackingValue", spec.args, line)
						return
					}
					vm.push(types.RetainValue(vm.Zone, bv))
					vm.bump()
					return
				case "getvalue":
					v, err := uc.GetValue()
					if err != nil {
						vm.raiseNativeError(err, cname+"::getValue", spec.args, line)
						return
					}
					vm.push(types.RetainValue(vm.Zone, v))
					vm.bump()
					return
				}
			}
		}
		rcc, ok := reflection.ClassConstantFromObject(vm.Zone, obj)
		if !ok {
			vm.raiseBuiltin(bytecode.ClassError, "Invalid ReflectionClassConstant object", 0)
			return
		}
		switch method {
		case "getname":
			vm.pushReflString(rcc.GetName())
			return
		case "ispublic":
			vm.push(types.NewBool(rcc.IsPublic()))
			vm.bump()
			return
		case "isprotected":
			vm.push(types.NewBool(rcc.IsProtected()))
			vm.bump()
			return
		case "isprivate":
			vm.push(types.NewBool(rcc.IsPrivate()))
			vm.bump()
			return
		case "isfinal":
			vm.push(types.NewBool(rcc.IsFinal()))
			vm.bump()
			return
		case "isenumcase":
			vm.push(types.NewBool(rcc.IsEnumCase()))
			vm.bump()
			return
		case "getmodifiers":
			vm.push(types.NewLong(rcc.GetModifiers()))
			vm.bump()
			return
		case "getvalue":
			val, err := rcc.GetValue()
			if err != nil {
				vm.raiseNativeError(err, cname+"::getValue", spec.args, line)
				return
			}
			vm.push(types.RetainValue(vm.Zone, val))
			vm.bump()
			return
		case "getdeclaringclass":
			dc, err := rcc.GetDeclaringClass()
			if err != nil {
				vm.raiseNativeError(err, cname+"::getDeclaringClass", spec.args, line)
				return
			}
			vm.push(dc.Object())
			vm.bump()
			return
		case "getattributes":
			fName := ""
			fFlags := int64(0)
			if len(spec.args) >= 1 && !types.Deref(vm.Zone, spec.args[0]).IsNull() {
				fName = vm.argCloneString(spec.args[0])
			}
			if len(spec.args) >= 2 {
				fFlags = vm.toLong(spec.args[1])
			}
			attrs := rcc.GetAttributes(fName, fFlags)
			objs := make([]types.Value, len(attrs))
			for i, a := range attrs {
				objs[i] = a.Object()
			}
			vm.pushReflObjects(objs)
			return
		case "__tostring":
			vm.pushReflString(rcc.String())
			return
		}

	case bytecode.ClassReflectionNamedType, bytecode.ClassReflectionUnionType, bytecode.ClassReflectionIntersectionType:
		rt, ok := reflection.TypeFromObject(vm.Zone, obj)
		if !ok {
			vm.raiseBuiltin(bytecode.ClassError, "Invalid ReflectionType object", 0)
			return
		}
		switch method {
		case "getname":
			vm.pushReflString(rt.GetName())
			return
		case "isbuiltin":
			vm.push(types.NewBool(rt.IsBuiltin()))
			vm.bump()
			return
		case "allowsnull":
			vm.push(types.NewBool(rt.AllowsNull()))
			vm.bump()
			return
		case "gettypes":
			sub, err := rt.GetTypes()
			if err != nil {
				vm.raiseNativeError(err, cname+"::getTypes", spec.args, line)
				return
			}
			objs := make([]types.Value, len(sub))
			for i, s := range sub {
				objs[i] = s.Object()
			}
			vm.pushReflObjects(objs)
			return
		case "__tostring":
			vm.pushReflString(rt.String())
			return
		}

	case bytecode.ClassReflectionAttribute:
		ra, ok := reflection.AttributeFromObject(vm.Zone, obj)
		if !ok {
			vm.raiseBuiltin(bytecode.ClassError, "Invalid ReflectionAttribute object", 0)
			return
		}
		switch method {
		case "getname":
			vm.pushReflString(ra.GetName())
			return
		case "getarguments":
			arr, err := ra.GetArguments()
			if err != nil {
				vm.raiseNativeError(err, "ReflectionAttribute::getArguments", spec.args, line)
				return
			}
			vm.push(arr)
			vm.bump()
			return
		case "newinstance":
			inst, err := ra.NewInstance()
			if err != nil {
				vm.raiseNativeError(err, "ReflectionAttribute::newInstance", spec.args, line)
				return
			}
			vm.push(inst)
			vm.bump()
			return
		case "__tostring":
			vm.pushReflString(ra.String())
			return
		}
	}

	vm.raiseBuiltin(bytecode.ClassError, "Call to undefined method "+cname+"::"+spec.method+"()", 0)
}

func (vm *VM) hasEnumValue(vals []types.Value) bool {
	for _, v := range vals {
		d := types.Deref(vm.Zone, v)
		switch d.Type {
		case types.TypeObject:
			return true
		case types.TypeArray:
			found := false
			types.ArrayForEach(vm.Zone, d, func(_, elem types.Value) bool {
				if vm.hasEnumValue([]types.Value{elem}) {
					found = true
					return false
				}
				return true
			})
			if found {
				return true
			}
		}
	}
	return false
}

func (vm *VM) varDumpEnumIntrinsic(spec callSpec) {
	defer vm.releaseArgs(spec.args)
	var buf strings.Builder
	for _, arg := range spec.args {
		vm.appendEnumAwareDump(&buf, types.Deref(vm.Zone, arg), 0)
	}
	if vm.Out != nil {
		_, _ = io.WriteString(vm.Out, buf.String())
	}
	vm.push(types.NewNull())
	vm.bump()
}

func (vm *VM) appendEnumAwareDump(buf *strings.Builder, v types.Value, indent int) {
	pad := strings.Repeat("  ", indent)
	switch v.Type {
	case types.TypeObject:
		info := types.ObjectInfo(vm.Zone, v)
		c := vm.classByID(info.ClassID)
		if c != nil && c.Kind == bytecode.KindEnum {
			caseName := ""
			if info.PropCount > 0 {
				nm := types.ObjectGet(vm.Zone, v, 0)
				if nm.Type == types.TypeString {
					caseName = types.StringData(vm.Zone, nm)
				}
			}
			buf.WriteString(pad)
			buf.WriteString("enum(")
			buf.WriteString(c.Name)
			buf.WriteString("::")
			buf.WriteString(caseName)
			buf.WriteString(")\n")
			return
		}
		type propDump struct {
			key string
			val types.Value
		}
		var props []propDump
		if c != nil && c.PropsResolved {
			for _, p := range c.ResolvedProps {
				if p.Static {
					continue
				}
				pv := types.ObjectGet(vm.Zone, v, p.Index)
				if pv.IsUndef() {
					continue
				}
				var k string
				switch p.Visibility {
				case bytecode.VisProtected:
					k = fmt.Sprintf("%q:protected", p.Name)
				case bytecode.VisPrivate:
					k = fmt.Sprintf("%q:%q:private", p.Name, vm.className(p.ClassID))
				default:
					k = fmt.Sprintf("%q", p.Name)
				}
				props = append(props, propDump{key: k, val: types.Deref(vm.Zone, pv)})
			}
		} else if c != nil {
			var chain [64]*bytecode.ClassProto
			nChain := 0
			for cur := c; cur != nil && nChain < 64; cur = vm.classByID(cur.ParentID) {
				chain[nChain] = cur
				nChain++
			}
			idx := uint32(0)
			if vm.isExceptionFamily(info.ClassID) {
				idx = 4
			}
			for i := nChain - 1; i >= 0; i-- {
				cur := chain[i]
				for _, p := range cur.Props {
					if p.Static {
						continue
					}
					if idx < info.PropCount {
						pv := types.ObjectGet(vm.Zone, v, idx)
						if !pv.IsUndef() {
							var k string
							switch p.Visibility {
							case bytecode.VisProtected:
								k = fmt.Sprintf("%q:protected", p.Name)
							case bytecode.VisPrivate:
								k = fmt.Sprintf("%q:%q:private", p.Name, vm.className(cur.ID))
							default:
								k = fmt.Sprintf("%q", p.Name)
							}
							props = append(props, propDump{key: k, val: types.Deref(vm.Zone, pv)})
						}
					}
					idx++
				}
			}
		}
		if info.DynOffset != types.EmptySlot {
			dynArr := types.Value{Type: types.TypeArray, Flags: types.FlagZoneRequest, DataSlot: info.DynOffset}
			types.ArrayForEach(vm.Zone, dynArr, func(k, dv types.Value) bool {
				var ks string
				if k.IsLong() {
					ks = fmt.Sprintf("%q", fmt.Sprintf("%d", k.ToLong()))
				} else if k.Type == types.TypeString {
					ks = fmt.Sprintf("%q", types.StringData(vm.Zone, k))
				}
				props = append(props, propDump{key: ks, val: types.Deref(vm.Zone, dv)})
				return true
			})
		}
		buf.WriteString(pad)
		buf.WriteString(fmt.Sprintf("object(%s)#%d (%d) {\n", vm.className(info.ClassID), v.DataSlot+1, len(props)))
		for _, pr := range props {
			buf.WriteString(pad)
			buf.WriteString("  [")
			buf.WriteString(pr.key)
			buf.WriteString("]=>\n")
			vm.appendEnumAwareDump(buf, pr.val, indent+1)
		}
		buf.WriteString(pad)
		buf.WriteString("}\n")
		return
	case types.TypeArray:
		if vm.hasEnumValue([]types.Value{v}) {
			buf.WriteString(pad)
			buf.WriteString(fmt.Sprintf("array(%d) {\n", types.ArrayLen(vm.Zone, v)))
			types.ArrayForEach(vm.Zone, v, func(k, elem types.Value) bool {
				buf.WriteString(pad)
				buf.WriteString("  [")
				if k.IsLong() {
					buf.WriteString(fmt.Sprintf("%d", k.ToLong()))
				} else {
					buf.WriteByte('"')
					if k.Type == types.TypeString {
						buf.WriteString(types.StringData(vm.Zone, k))
					}
					buf.WriteByte('"')
				}
				buf.WriteString("]=>\n")
				vm.appendEnumAwareDump(buf, types.Deref(vm.Zone, elem), indent+1)
				return true
			})
			buf.WriteString(pad)
			buf.WriteString("}\n")
			return
		}
	}
	if native, ok := stdlib.Lookup("var_dump"); ok {
		var sub strings.Builder
		_, _ = native(vm.Zone, &sub, []types.Value{v})
		lines := strings.SplitSeq(strings.TrimSuffix(sub.String(), "\n"), "\n")
		for line := range lines {
			buf.WriteString(pad)
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
}
