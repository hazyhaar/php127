package ext

/*
#cgo LDFLAGS: -lpcre2-8
#define PCRE2_CODE_UNIT_WIDTH 8
#include <pcre2.h>
#include <stdint.h>

static uint32_t php127_pcre2_capture_count(const pcre2_code_8 *code) {
	uint32_t count = 0;
	pcre2_pattern_info_8(code, PCRE2_INFO_CAPTURECOUNT, &count);
	return count;
}

static uint32_t php127_pcre2_name_count(const pcre2_code_8 *code) {
	uint32_t count = 0;
	pcre2_pattern_info_8(code, PCRE2_INFO_NAMECOUNT, &count);
	return count;
}

static uint32_t php127_pcre2_name_entry_size(const pcre2_code_8 *code) {
	uint32_t size = 0;
	pcre2_pattern_info_8(code, PCRE2_INFO_NAMEENTRYSIZE, &size);
	return size;
}

static PCRE2_SPTR8 php127_pcre2_name_table(const pcre2_code_8 *code) {
	PCRE2_SPTR8 table = NULL;
	pcre2_pattern_info_8(code, PCRE2_INFO_NAMETABLE, &table);
	return table;
}

// Contexte de correspondance aux limites par défaut de PHP 8.3 : pcre.backtrack_limit = 1000000,
// pcre.recursion_limit = 100000. Il n'est que lu par pcre2_match, donc partageable entre fils.
static pcre2_match_context_8 *php127_pcre2_match_context(void) {
	pcre2_match_context_8 *ctx = pcre2_match_context_create_8(NULL);
	if (ctx != NULL) {
		pcre2_set_match_limit_8(ctx, 1000000);
		pcre2_set_depth_limit_8(ctx, 100000);
	}
	return ctx;
}
*/
import "C"

import (
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

type pcreCompiled struct {
	mu      sync.Mutex
	code    *C.pcre2_code_8
	options C.uint32_t
	utf8    bool
	groups  int
	names   []string
}

var pcreCache sync.Map

var pcreMatchContext = sync.OnceValue(func() *C.pcre2_match_context_8 { return C.php127_pcre2_match_context() })

// pcreErrorCode traduit un code d'échec de pcre2_match en code de preg_last_error(), comme pcre_handle_exec_error.
func pcreErrorCode(rc C.int) int64 {
	switch {
	case rc == C.PCRE2_ERROR_MATCHLIMIT:
		return pregBacktrackLimitError
	case rc == C.PCRE2_ERROR_DEPTHLIMIT:
		return pregRecursionLimitError
	case rc <= C.PCRE2_ERROR_UTF8_ERR1 && rc >= C.PCRE2_ERROR_UTF8_ERR21:
		return pregBadUTF8Error
	case rc == C.PCRE2_ERROR_BADUTFOFFSET:
		return pregBadUTF8OffsetError
	case rc == C.PCRE2_ERROR_JIT_STACKLIMIT:
		return pregJITStackLimitError
	default:
		return pregInternalError
	}
}

func compilePCRE2(pattern string) (*pcreCompiled, bool) {
	if v, ok := pcreCache.Load(pattern); ok {
		if v == nil {
			return nil, false
		}
		return v.(*pcreCompiled), true
	}
	p, ok := parsePatternAllowEmpty(pattern)
	if !ok {
		return nil, false
	}
	var opts C.uint32_t
	isUTF := false
	for i := 0; i < len(p.flags); i++ {
		switch p.flags[i] {
		case 'i':
			opts |= C.PCRE2_CASELESS
		case 'm':
			opts |= C.PCRE2_MULTILINE
		case 's':
			opts |= C.PCRE2_DOTALL
		case 'x':
			opts |= C.PCRE2_EXTENDED
		case 'A':
			opts |= C.PCRE2_ANCHORED
		case 'D':
			opts |= C.PCRE2_DOLLAR_ENDONLY
		case 'U':
			opts |= C.PCRE2_UNGREEDY
		case 'u':
			opts |= C.PCRE2_UTF | C.PCRE2_UCP
			isUTF = true
		case 'J':
			opts |= C.PCRE2_DUPNAMES
		case 'S':
			// Study modifier accepted as no-op in PCRE2
		default:
			return nil, false
		}
	}
	var errCode C.int
	var errOffset C.PCRE2_SIZE
	var patPtr C.PCRE2_SPTR8
	var rawPat []byte
	if len(p.body) > 0 {
		rawPat = []byte(p.body)
		patPtr = (C.PCRE2_SPTR8)(unsafe.Pointer(&rawPat[0]))
	}
	code := C.pcre2_compile_8(patPtr, C.PCRE2_SIZE(len(rawPat)), opts, &errCode, &errOffset, nil)
	if code == nil {
		return nil, false
	}
	capCount := int(C.php127_pcre2_capture_count(code))
	names := make([]string, capCount+1)
	nameCount := int(C.php127_pcre2_name_count(code))
	if nameCount > 0 {
		entrySize := int(C.php127_pcre2_name_entry_size(code))
		tablePtr := C.php127_pcre2_name_table(code)
		if tablePtr != nil && entrySize >= 3 {
			totalBytes := nameCount * entrySize
			table := unsafe.Slice((*byte)(unsafe.Pointer(tablePtr)), totalBytes)
			for i := 0; i < nameCount; i++ {
				entry := table[i*entrySize : (i+1)*entrySize]
				groupNum := (int(entry[0]) << 8) | int(entry[1])
				nameBytes := entry[2:]
				zeroIdx := 0
				for zeroIdx < len(nameBytes) && nameBytes[zeroIdx] != 0 {
					zeroIdx++
				}
				nameStr := string(nameBytes[:zeroIdx])
				_ = C.pcre2_substring_number_from_name_8(code, (C.PCRE2_SPTR8)(unsafe.Pointer(&entry[2])))
				if groupNum >= 1 && groupNum <= capCount {
					names[groupNum] = nameStr
				}
			}
		}
	}
	compiled := &pcreCompiled{
		code:    code,
		options: opts,
		utf8:    isUTF,
		groups:  capCount + 1,
		names:   names,
	}
	actual, loaded := pcreCache.LoadOrStore(strings.Clone(pattern), compiled)
	if loaded {
		C.pcre2_code_free_8(code)
		return actual.(*pcreCompiled), true
	}
	return compiled, true
}

func parsePatternAllowEmpty(pattern string) (parsedPat, bool) {
	if pattern == "" {
		return parsedPat{}, false
	}
	start := 0
	for start < len(pattern) && isSpace(pattern[start]) {
		start++
	}
	if start >= len(pattern) {
		return parsedPat{}, false
	}
	delim := pattern[start]
	if delim == 0 || delim == '\\' || isAlnum(delim) || isSpace(delim) {
		return parsedPat{}, false
	}
	end := delim
	bracketed := false
	switch delim {
	case '(':
		end = ')'
		bracketed = true
	case '{':
		end = '}'
		bracketed = true
	case '[':
		end = ']'
		bracketed = true
	case '<':
		end = '>'
		bracketed = true
	}
	var body strings.Builder
	i := start + 1
	depth := 1
	found := false
	for i < len(pattern) {
		c := pattern[i]
		if c == '\\' && i+1 < len(pattern) {
			nxt := pattern[i+1]
			if !bracketed && nxt == end {
				body.WriteByte(nxt)
				i += 2
				continue
			}
			if bracketed && (nxt == delim || nxt == end) {
				body.WriteByte('\\')
				body.WriteByte(nxt)
				i += 2
				continue
			}
			body.WriteByte('\\')
			body.WriteByte(nxt)
			i += 2
			continue
		}
		if bracketed && c == delim {
			depth++
			body.WriteByte(c)
			i++
			continue
		}
		if c == end {
			depth--
			if depth == 0 {
				found = true
				i++
				break
			}
		}
		body.WriteByte(c)
		i++
	}
	if !found {
		return parsedPat{}, false
	}
	var flags strings.Builder
	for j := i; j < len(pattern); j++ {
		if isSpace(pattern[j]) || pattern[j] == '\n' {
			continue
		}
		if !isModifier(pattern[j]) {
			return parsedPat{}, false
		}
		flags.WriteByte(pattern[j])
	}
	return parsedPat{body: body.String(), flags: flags.String()}, true
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\v' || c == '\f'
}

// findOne rend la première correspondance (nil sans correspondance) et le code de preg_last_error() de l'appel.
func (c *pcreCompiled) findOne(subject string, startOffset int) ([]int, int64) {
	if c.utf8 && !utf8.ValidString(subject) {
		return nil, pregBadUTF8Error
	}
	if startOffset < 0 || startOffset > len(subject) {
		return nil, pregInternalError
	}
	var subPtr C.PCRE2_SPTR8
	var rawSub []byte
	if len(subject) > 0 {
		rawSub = []byte(subject)
		subPtr = (C.PCRE2_SPTR8)(unsafe.Pointer(&rawSub[0]))
	}
	md := C.pcre2_match_data_create_from_pattern_8(c.code, nil)
	if md == nil {
		return nil, pregInternalError
	}
	defer C.pcre2_match_data_free_8(md)

	rc := C.pcre2_match_8(c.code, subPtr, C.PCRE2_SIZE(len(rawSub)), C.PCRE2_SIZE(startOffset), 0, md, pcreMatchContext())
	if rc == C.PCRE2_ERROR_NOMATCH {
		return nil, pregNoError
	}
	if rc < 0 {
		return nil, pcreErrorCode(rc)
	}
	return c.extractLocs(md, int(rc)), pregNoError
}

// findAll rend toutes les correspondances et le code de preg_last_error() de l'appel.
func (c *pcreCompiled) findAll(subject string, startOffset int) ([][]int, int64) {
	if c.utf8 && !utf8.ValidString(subject) {
		return nil, pregBadUTF8Error
	}
	if startOffset < 0 || startOffset > len(subject) {
		return nil, pregInternalError
	}
	var subPtr C.PCRE2_SPTR8
	var rawSub []byte
	if len(subject) > 0 {
		rawSub = []byte(subject)
		subPtr = (C.PCRE2_SPTR8)(unsafe.Pointer(&rawSub[0]))
	}
	md := C.pcre2_match_data_create_from_pattern_8(c.code, nil)
	if md == nil {
		return nil, pregInternalError
	}
	defer C.pcre2_match_data_free_8(md)

	var all [][]int
	offset := startOffset
	var extraOpts C.uint32_t
	for {
		rc := C.pcre2_match_8(c.code, subPtr, C.PCRE2_SIZE(len(rawSub)), C.PCRE2_SIZE(offset), extraOpts, md, pcreMatchContext())
		if rc == C.PCRE2_ERROR_NOMATCH {
			if extraOpts != 0 && offset < len(rawSub) {
				if c.utf8 {
					_, step := utf8.DecodeRuneInString(subject[offset:])
					if step < 1 {
						step = 1
					}
					offset += step
				} else {
					offset++
				}
				extraOpts = 0
				continue
			}
			break
		}
		if rc < 0 {
			return nil, pcreErrorCode(rc)
		}
		locs := c.extractLocs(md, int(rc))
		all = append(all, locs)
		if (c.options & C.PCRE2_ANCHORED) != 0 {
			break
		}
		if locs[0] == locs[1] {
			if locs[1] == len(rawSub) {
				break
			}
			extraOpts = C.PCRE2_NOTEMPTY_ATSTART | C.PCRE2_ANCHORED
			offset = locs[1]
		} else {
			extraOpts = 0
			offset = locs[1]
		}
	}
	return all, pregNoError
}

func (c *pcreCompiled) extractLocs(md *C.pcre2_match_data_8, rc int) []int {
	ovecPtr := C.pcre2_get_ovector_pointer_8(md)
	count := c.groups
	if rc > 0 && rc < count {
		count = rc
	}
	ovec := unsafe.Slice((*C.PCRE2_SIZE)(unsafe.Pointer(ovecPtr)), c.groups*2)
	locs := make([]int, c.groups*2)
	for i := 0; i < c.groups; i++ {
		if i < count && ovec[2*i] != C.PCRE2_UNSET {
			locs[2*i] = int(ovec[2*i])
			locs[2*i+1] = int(ovec[2*i+1])
		} else {
			locs[2*i] = -1
			locs[2*i+1] = -1
		}
	}
	return locs
}

func resolveMatchOffset(subject string, offset int64) (int, bool) {
	if offset < 0 {
		offset += int64(len(subject))
		if offset < 0 {
			return 0, false
		}
	}
	if offset > int64(len(subject)) {
		return 0, false
	}
	return int(offset), true
}

func workerMatch(z *zone.Zone, op, pattern, subject string, flags, offset int64, ref types.Value) (types.Value, error) {
	compiled, ok := compilePCRE2(pattern)
	if !ok {
		setPregError(z, pregInternalError)
		return types.NewBool(false), nil
	}
	startOffset, ok := resolveMatchOffset(subject, offset)
	if !ok {
		setPregError(z, pregInternalError)
		empty, err := newArray(z)
		if err != nil {
			return types.Value{}, err
		}
		writeRef(z, ref, empty)
		return types.NewBool(false), nil
	}
	offCap := flags&flagOffsetCapture != 0
	nullUn := flags&flagUnmatchedAsNull != 0
	if op == "match" {
		locs, code := compiled.findOne(subject, startOffset)
		setPregError(z, code)
		if code != pregNoError {
			empty, err := newArray(z)
			if err != nil {
				return types.Value{}, err
			}
			writeRef(z, ref, empty)
			return types.NewBool(false), nil
		}
		if locs == nil {
			empty, err := newArray(z)
			if err != nil {
				return types.Value{}, err
			}
			writeRef(z, ref, empty)
			return types.NewLong(0), nil
		}
		set, err := matchSetPCRE(z, subject, locs, compiled.names, offCap, nullUn)
		if err != nil {
			return types.Value{}, err
		}
		writeRef(z, ref, set)
		return types.NewLong(1), nil
	}
	all, code := compiled.findAll(subject, startOffset)
	setPregError(z, code)
	if code != pregNoError {
		empty, err := newArray(z)
		if err != nil {
			return types.Value{}, err
		}
		writeRef(z, ref, empty)
		return types.NewBool(false), nil
	}
	arr, n, err := matchAllPCRE(z, subject, all, compiled.names, flags, offCap, nullUn)
	if err != nil {
		return types.Value{}, err
	}
	writeRef(z, ref, arr)
	return types.NewLong(int64(n)), nil
}

func matchSetPCRE(z *zone.Zone, subject string, locs []int, names []string, offCap, nullUn bool) (types.Value, error) {
	a, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	n := len(locs) / 2
	lastMatched := 0
	if !nullUn {
		for i := n - 1; i >= 0; i-- {
			if locs[2*i] >= 0 {
				lastMatched = i
				break
			}
		}
	} else {
		lastMatched = n - 1
	}
	for i := 0; i <= lastMatched; i++ {
		capVal, err := captureVal(z, subject, locs[2*i], locs[2*i+1], offCap, nullUn)
		if err != nil {
			return types.Value{}, err
		}
		if i > 0 && i < len(names) && names[i] != "" {
			// Under /J (DUPNAMES), a later group with the same name only overwrites if it matched
			// or if no earlier group with that name matched yet.
			existing, exists, _ := getStrKey(z, a, names[i])
			if !exists || locs[2*i] >= 0 || isUnmatchedCapture(z, existing, offCap) {
				if err = setStrKey(z, &a, names[i], capVal); err != nil {
					return types.Value{}, err
				}
			}
		}
		if err = appendVal(z, &a, capVal); err != nil {
			return types.Value{}, err
		}
	}
	return a, nil
}

func getStrKey(z *zone.Zone, a types.Value, key string) (types.Value, bool, error) {
	k, err := newStr(z, key)
	if err != nil {
		return types.Value{}, false, err
	}
	return types.ArrayGet(z, a, k, nil)
}

func isUnmatchedCapture(z *zone.Zone, v types.Value, offCap bool) bool {
	v = deref(z, v)
	if !offCap {
		if v.IsNull() {
			return true
		}
		if v.Type == types.TypeString {
			return types.StringData(z, v) == ""
		}
		return false
	}
	if v.Type == types.TypeArray {
		off, ok, _ := types.ArrayGet(z, v, types.NewLong(1), nil)
		if ok && off.Type == types.TypeLong && off.ToLong() == -1 {
			return true
		}
	}
	return false
}

func matchAllPCRE(z *zone.Zone, subject string, all [][]int, names []string, flags int64, offCap, nullUn bool) (types.Value, int, error) {
	if flags&flagSetOrder != 0 {
		out, err := newArray(z)
		if err != nil {
			return types.Value{}, 0, err
		}
		for _, locs := range all {
			set, err := matchSetPCRE(z, subject, locs, names, offCap, nullUn)
			if err != nil {
				return types.Value{}, 0, err
			}
			if err = appendVal(z, &out, set); err != nil {
				return types.Value{}, 0, err
			}
		}
		return out, len(all), nil
	}
	return matchAll(z, subject, all, names, flags, offCap, nullUn)
}

func workerReplace(z *zone.Zone, pattern, repl, subject string, limit int64, countRef types.Value) (types.Value, error) {
	out, count, ok, err := pcreReplaceSingle(z, pattern, repl, subject, limit)
	if err != nil {
		return types.Value{}, err
	}
	if !ok {
		writeRef(z, countRef, types.NewLong(0))
		return types.NewNull(), nil
	}
	writeRef(z, countRef, types.NewLong(count))
	return newStr(z, out)
}

func pcreReplaceSingle(z *zone.Zone, pattern, repl, subject string, limit int64) (string, int64, bool, error) {
	compiled, ok := compilePCRE2(pattern)
	if !ok {
		setPregError(z, pregInternalError)
		return "", 0, false, nil
	}
	if limit == 0 {
		return subject, 0, true, nil
	}
	all, code := compiled.findAll(subject, 0)
	setPregError(z, code)
	if code != pregNoError {
		return "", 0, false, nil
	}
	if limit > 0 && int64(len(all)) > limit {
		all = all[:limit]
	}
	var b strings.Builder
	pos := 0
	count := int64(0)
	for _, locs := range all {
		if locs[0] < pos {
			continue
		}
		b.WriteString(subject[pos:locs[0]])
		b.WriteString(expandPCRE2Repl(repl, subject, locs))
		pos = locs[1]
		count++
	}
	b.WriteString(subject[pos:])
	return b.String(), count, true, nil
}

func expandPCRE2Repl(repl, subject string, locs []int) string {
	nGroups := len(locs) / 2
	var b strings.Builder
	for i := 0; i < len(repl); i++ {
		c := repl[i]
		if c == '\\' && i+1 < len(repl) {
			nxt := repl[i+1]
			if nxt >= '0' && nxt <= '9' {
				j := i + 1
				for j < len(repl) && j < i+3 && repl[j] >= '0' && repl[j] <= '9' {
					j++
				}
				idx, _ := strconv.Atoi(repl[i+1 : j])
				if idx < nGroups {
					b.WriteString(groupText(subject, locs, idx))
				}
				i = j - 1
				continue
			}
			if nxt == '\\' || nxt == '$' {
				b.WriteByte(nxt)
				i++
				continue
			}
			b.WriteByte('\\')
			b.WriteByte(nxt)
			i++
			continue
		}
		if c == '$' && i+1 < len(repl) {
			if repl[i+1] == '{' {
				end := strings.IndexByte(repl[i+2:], '}')
				if end > 0 {
					token := repl[i+2 : i+2+end]
					allDigits := true
					for k := 0; k < len(token); k++ {
						if token[k] < '0' || token[k] > '9' {
							allDigits = false
							break
						}
					}
					if allDigits && len(token) <= 2 {
						idx, _ := strconv.Atoi(token)
						if idx < nGroups {
							b.WriteString(groupText(subject, locs, idx))
						}
						i = i + 2 + end
						continue
					}
				}
			}
			if repl[i+1] >= '0' && repl[i+1] <= '9' {
				j := i + 1
				for j < len(repl) && j < i+3 && repl[j] >= '0' && repl[j] <= '9' {
					j++
				}
				idx, _ := strconv.Atoi(repl[i+1 : j])
				if idx < nGroups {
					b.WriteString(groupText(subject, locs, idx))
				}
				i = j - 1
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

func workerSplit(z *zone.Zone, pattern, subject string, limit, flags int64) (types.Value, error) {
	compiled, ok := compilePCRE2(pattern)
	if !ok {
		setPregError(z, pregInternalError)
		return types.NewBool(false), nil
	}
	all, code := compiled.findAll(subject, 0)
	setPregError(z, code)
	if code != pregNoError {
		return types.NewBool(false), nil
	}
	noEmpty := flags&flagSplitNoEmpty != 0
	delim := flags&flagSplitDelim != 0
	offCap := flags&flagSplitOffset != 0
	maxPieces := -1
	if limit > 0 {
		maxPieces = int(limit)
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	pos := 0
	emitted := 0
	for _, locs := range all {
		if maxPieces > 0 && emitted+1 >= maxPieces {
			break
		}
		if locs[0] < pos {
			continue
		}
		pieceEmpty := pos == locs[0]
		if !pieceEmpty || !noEmpty {
			if err = emitSplit(z, &out, subject, pos, locs[0], noEmpty, offCap); err != nil {
				return types.Value{}, err
			}
			emitted++
		}
		if delim {
			lastDelim := 0
			for g := len(locs)/2 - 1; g >= 1; g-- {
				if locs[2*g] >= 0 {
					lastDelim = g
					break
				}
			}
			for g := 1; g <= lastDelim; g++ {
				start, end := locs[2*g], locs[2*g+1]
				if start < 0 {
					if !noEmpty {
						if err = emitSplit(z, &out, subject, 0, 0, false, offCap); err != nil {
							return types.Value{}, err
						}
					}
					continue
				}
				if err = emitSplit(z, &out, subject, start, end, noEmpty, offCap); err != nil {
					return types.Value{}, err
				}
			}
		}
		pos = locs[1]
	}
	if err = emitSplit(z, &out, subject, pos, len(subject), noEmpty, offCap); err != nil {
		return types.Value{}, err
	}
	return out, nil
}

// workerGrep s'arrête, comme PHP 8.3, au premier élément dont l'exécution échoue et rend les éléments retenus jusque-là.
func workerGrep(z *zone.Zone, pattern string, arr types.Value, flags int64) (types.Value, error) {
	compiled, ok := compilePCRE2(pattern)
	if !ok {
		setPregError(z, pregInternalError)
		return types.NewBool(false), nil
	}
	invert := flags&flagGrepInvert != 0
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	var walkErr error
	types.ArrayForEach(z, arr, func(k, v types.Value) bool {
		s, err := asString(z, v, "preg_grep", "2", "array")
		if err != nil {
			s = ""
		}
		locs, code := compiled.findOne(s, 0)
		setPregError(z, code)
		if code != pregNoError {
			return false
		}
		matched := locs != nil
		if invert {
			matched = !matched
		}
		if matched {
			if err = types.ArraySet(z, &out, k, v, nil); err != nil {
				walkErr = err
				return false
			}
		}
		return true
	})
	if walkErr != nil {
		return types.Value{}, walkErr
	}
	return out, nil
}

// PCREFindAll compile le motif PCRE2, relève jusqu'à limit correspondances dans subject (toutes si limit < 0, aucune si limit == 0) et consigne le code de preg_last_error() dans la Zone.
func PCREFindAll(z *zone.Zone, pattern, subject string, limit int) ([][]int, []string, bool) {
	compiled, ok := compilePCRE2(pattern)
	if !ok {
		setPregError(z, pregInternalError)
		return nil, nil, false
	}
	if limit == 0 {
		setPregError(z, pregNoError)
		return nil, compiled.names, true
	}
	all, code := compiled.findAll(subject, 0)
	setPregError(z, code)
	if code != pregNoError {
		return nil, nil, false
	}
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, compiled.names, true
}
