package ext

import (
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	flagOffsetCapture   int64 = 256
	flagUnmatchedAsNull int64 = 512
	flagPatternOrder    int64 = 1
	flagSetOrder        int64 = 2
	flagSplitNoEmpty    int64 = 1
	flagSplitDelim      int64 = 2
	flagSplitOffset     int64 = 4
	flagGrepInvert      int64 = 1
)

var (
	re2Hits   atomic.Int64
	pcre2Hits atomic.Int64
	reCache   sync.Map
)

type parsedPat struct {
	body  string
	flags string
}

func nativePregMatch(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("preg_match", len(args), 2, 5); err != nil {
		return types.Value{}, err
	}
	pattern, subject, flags, offset, err := pregArgs(z, args, "preg_match", 0)
	if err != nil {
		return types.Value{}, err
	}
	ref := arg(args, 2)
	if offset > int64(len(subject)) {
		empty, err := newArray(z)
		if err != nil {
			return types.Value{}, err
		}
		writeRef(z, ref, empty)
		return types.NewBool(false), nil
	}
	if p, ok := parsePattern(pattern); ok && offset >= 0 && flagsKnown(flags, flagOffsetCapture|flagUnmatchedAsNull) && re2Safe(p, subject, int(offset)) {
		if v, err, used := goMatch(z, p, subject, flags, int(offset), ref, false); used {
			re2Hits.Add(1)
			return v, err
		}
	}
	pcre2Hits.Add(1)
	return workerMatch(z, "match", pattern, subject, flags, offset, ref)
}

func nativePregMatchAll(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("preg_match_all", len(args), 2, 5); err != nil {
		return types.Value{}, err
	}
	pattern, subject, flags, offset, err := pregArgs(z, args, "preg_match_all", flagPatternOrder)
	if err != nil {
		return types.Value{}, err
	}
	ref := arg(args, 2)
	if offset > int64(len(subject)) {
		empty, err := newArray(z)
		if err != nil {
			return types.Value{}, err
		}
		writeRef(z, ref, empty)
		return types.NewBool(false), nil
	}
	known := flagPatternOrder | flagSetOrder | flagOffsetCapture | flagUnmatchedAsNull
	if p, ok := parsePattern(pattern); ok && offset >= 0 && flagsKnown(flags, known) && re2Safe(p, subject, int(offset)) {
		if v, err, used := goMatch(z, p, subject, flags, int(offset), ref, true); used {
			re2Hits.Add(1)
			return v, err
		}
	}
	pcre2Hits.Add(1)
	return workerMatch(z, "match_all", pattern, subject, flags, offset, ref)
}

func nativePregReplace(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("preg_replace", len(args), 3, 5); err != nil {
		return types.Value{}, err
	}
	patVal := deref(z, args[0])
	repVal := deref(z, args[1])
	subVal := deref(z, args[2])
	if repVal.Type == types.TypeArray && patVal.Type != types.TypeArray {
		if patVal.Type == types.TypeObject {
			return types.Value{}, typeErr("preg_replace", "1", "pattern", "array|string", "object")
		}
		return types.Value{}, TypeError{"preg_replace(): Argument #1 ($pattern) must be of type array when argument #2 ($replacement) is an array, string given"}
	}
	var patterns []string
	if patVal.Type == types.TypeArray {
		var walkErr error
		types.ArrayForEach(z, patVal, func(_, v types.Value) bool {
			s, err := asString(z, v, "preg_replace", "1", "pattern")
			if err != nil {
				walkErr = err
				return false
			}
			patterns = append(patterns, s)
			return true
		})
		if walkErr != nil {
			return types.Value{}, walkErr
		}
	} else {
		p, err := asString(z, patVal, "preg_replace", "1", "pattern")
		if err != nil {
			return types.Value{}, err
		}
		patterns = []string{p}
	}
	var repls []string
	repIsArray := repVal.Type == types.TypeArray
	if repIsArray {
		var walkErr error
		types.ArrayForEach(z, repVal, func(_, v types.Value) bool {
			s, err := asString(z, v, "preg_replace", "2", "replacement")
			if err != nil {
				walkErr = err
				return false
			}
			repls = append(repls, s)
			return true
		})
		if walkErr != nil {
			return types.Value{}, walkErr
		}
	} else {
		r, err := asString(z, repVal, "preg_replace", "2", "replacement")
		if err != nil {
			return types.Value{}, err
		}
		repls = []string{r}
	}
	if subVal.Type != types.TypeArray {
		if _, err := asString(z, subVal, "preg_replace", "3", "subject"); err != nil {
			return types.Value{}, err
		}
	}
	limit, err := optInt(z, args, 3, -1, "preg_replace", "4", "limit")
	if err != nil {
		return types.Value{}, err
	}
	countRef := arg(args, 4)
	if patVal.Type != types.TypeArray && !repIsArray && subVal.Type != types.TypeArray {
		pattern := patterns[0]
		repl := repls[0]
		subject, _ := asString(z, subVal, "preg_replace", "3", "subject")
		if p, ok := parsePattern(pattern); ok && re2Safe(p, subject, 0) {
			if v, err, used := goReplace(z, p, repl, subject, limit, countRef); used {
				re2Hits.Add(1)
				return v, err
			}
		}
		pcre2Hits.Add(1)
		return workerReplace(z, pattern, repl, subject, limit, countRef)
	}
	replaceOneSubject := func(subject string) (string, int64, bool, error) {
		cur := subject
		var subCount int64
		for i, pat := range patterns {
			rep := ""
			if repIsArray {
				if i < len(repls) {
					rep = repls[i]
				}
			} else {
				rep = repls[0]
			}
			pcre2Hits.Add(1)
			next, c, ok, err := pcreReplaceSingle(z, pat, rep, cur, limit)
			if err != nil {
				return "", 0, false, err
			}
			if !ok {
				return "", subCount, false, nil
			}
			cur = next
			subCount += c
		}
		return cur, subCount, true, nil
	}
	if subVal.Type != types.TypeArray {
		subject, _ := asString(z, subVal, "preg_replace", "3", "subject")
		res, totalCount, ok, err := replaceOneSubject(subject)
		if err != nil {
			return types.Value{}, err
		}
		writeRef(z, countRef, types.NewLong(totalCount))
		if !ok {
			return types.NewNull(), nil
		}
		return newStr(z, res)
	}
	outArr, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	var totalCount int64
	var walkErr error
	failed := false
	types.ArrayForEach(z, subVal, func(k, v types.Value) bool {
		s, err := asString(z, v, "preg_replace", "3", "subject")
		if err != nil {
			walkErr = err
			return false
		}
		res, c, ok, err := replaceOneSubject(s)
		if err != nil {
			walkErr = err
			return false
		}
		totalCount += c
		if !ok {
			failed = true
			return false
		}
		sv, err := newStr(z, res)
		if err != nil {
			walkErr = err
			return false
		}
		if err = types.ArraySet(z, &outArr, k, sv, nil); err != nil {
			walkErr = err
			return false
		}
		return true
	})
	if walkErr != nil {
		return types.Value{}, walkErr
	}
	writeRef(z, countRef, types.NewLong(totalCount))
	if failed {
		return types.NewNull(), nil
	}
	return outArr, nil
}

func nativePregSplit(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("preg_split", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	pattern, err := asString(z, args[0], "preg_split", "1", "pattern")
	if err != nil {
		return types.Value{}, err
	}
	subject, err := asString(z, args[1], "preg_split", "2", "subject")
	if err != nil {
		return types.Value{}, err
	}
	limit, err := optInt(z, args, 2, -1, "preg_split", "3", "limit")
	if err != nil {
		return types.Value{}, err
	}
	flags, err := optInt(z, args, 3, 0, "preg_split", "4", "flags")
	if err != nil {
		return types.Value{}, err
	}
	known := flagSplitNoEmpty | flagSplitDelim | flagSplitOffset
	if p, ok := parsePattern(pattern); ok && flagsKnown(flags, known) && re2Safe(p, subject, 0) {
		if v, err, used := goSplit(z, p, subject, limit, flags); used {
			re2Hits.Add(1)
			return v, err
		}
	}
	pcre2Hits.Add(1)
	return workerSplit(z, pattern, subject, limit, flags)
}

func nativePregQuote(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("preg_quote", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], "preg_quote", "1", "str")
	if err != nil {
		return types.Value{}, err
	}
	delim := ""
	if len(args) >= 2 && !args[1].IsUndef() && !deref(z, args[1]).IsNull() {
		delim, err = asString(z, args[1], "preg_quote", "2", "delimiter")
		if err != nil {
			return types.Value{}, err
		}
	}
	return newStr(z, quotePattern(s, delim))
}

func nativePregGrep(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("preg_grep", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	pattern, err := asString(z, args[0], "preg_grep", "1", "pattern")
	if err != nil {
		return types.Value{}, err
	}
	if deref(z, args[1]).Type != types.TypeArray {
		return types.Value{}, typeErr("preg_grep", "2", "array", "array", typeKind(z, args[1]))
	}
	flags, err := optInt(z, args, 2, 0, "preg_grep", "3", "flags")
	if err != nil {
		return types.Value{}, err
	}
	if p, ok := parsePattern(pattern); ok && flagsKnown(flags, flagGrepInvert) && re2Safe(p, "", 0) && grepASCII(z, args[1], p) {
		if v, err, used := goGrep(z, p, args[1], flags); used {
			re2Hits.Add(1)
			return v, err
		}
	}
	pcre2Hits.Add(1)
	return workerGrep(z, pattern, args[1], flags)
}

func pregArgs(z *zone.Zone, args []types.Value, name string, flagDefault int64) (string, string, int64, int64, error) {
	pattern, err := asString(z, args[0], name, "1", "pattern")
	if err != nil {
		return "", "", 0, 0, err
	}
	subject, err := asString(z, args[1], name, "2", "subject")
	if err != nil {
		return "", "", 0, 0, err
	}
	flags, err := optInt(z, args, 3, flagDefault, name, "4", "flags")
	if err != nil {
		return "", "", 0, 0, err
	}
	offset, err := optInt(z, args, 4, 0, name, "5", "offset")
	if err != nil {
		return "", "", 0, 0, err
	}
	return pattern, subject, flags, offset, nil
}

func flagsKnown(flags, known int64) bool { return flags & ^known == 0 }

func parsePattern(pattern string) (parsedPat, bool) {
	if pattern == "" {
		return parsedPat{}, false
	}
	delim := pattern[0]
	if delim == 0 || delim == '\\' || isAlnum(delim) {
		return parsedPat{}, false
	}
	end := delim
	switch delim {
	case '(':
		end = ')'
	case '{':
		end = '}'
	case '[':
		end = ']'
	case '<':
		end = '>'
	}
	var body strings.Builder
	i := 1
	found := false
	for i < len(pattern) {
		c := pattern[i]
		if c == '\\' && i+1 < len(pattern) {
			nxt := pattern[i+1]
			if nxt == end {
				body.WriteByte(nxt)
				i += 2
				continue
			}
			body.WriteByte('\\')
			body.WriteByte(nxt)
			i += 2
			continue
		}
		if c == end {
			found = true
			i++
			break
		}
		body.WriteByte(c)
		i++
	}
	if !found || body.Len() == 0 {
		return parsedPat{}, false
	}
	flags := pattern[i:]
	for j := 0; j < len(flags); j++ {
		if !isModifier(flags[j]) {
			return parsedPat{}, false
		}
	}
	return parsedPat{body: body.String(), flags: flags}, true
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

func isModifier(c byte) bool {
	switch c {
	case 'i', 'm', 's', 'x', 'u', 'U', 'D', 'A', 'S', 'J':
		return true
	default:
		return false
	}
}

func re2Safe(p parsedPat, subject string, offset int) bool {
	if strings.ContainsAny(p.flags, "UJ") {
		return false
	}
	if !utf8.ValidString(p.body) || !utf8.ValidString(subject) {
		return false
	}
	if offset > 0 && offset <= len(subject) && !utf8.ValidString(subject[:offset]) {
		return false
	}
	if pcreOnly(p.body) {
		return false
	}
	ascii := isASCII(p.body) && isASCII(subject)
	if !ascii {
		if !strings.Contains(p.flags, "u") || strings.Contains(p.flags, "i") || hasWordish(p.body) {
			return false
		}
	}
	if strings.Contains(p.body, "$") && !strings.Contains(p.flags, "D") && strings.HasSuffix(subject, "\n") {
		return false
	}
	return true
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func pcreOnly(body string) bool {
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\\' && i+1 < len(body) {
			n := body[i+1]
			if (n >= '1' && n <= '9') || n == 'C' || n == 'g' {
				return true
			}
			i++
			continue
		}
		if c == '(' && i+2 < len(body) && body[i+1] == '?' {
			rest := body[i+2:]
			if strings.HasPrefix(rest, "P<") || strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, "!") || inlineFlag(rest) {
				continue
			}
			return true
		}
		if (c == '*' || c == '+' || c == '?' || c == '}') && i+1 < len(body) && body[i+1] == '+' {
			return true
		}
	}
	return false
}

func inlineFlag(rest string) bool {
	if rest == "" {
		return false
	}
	switch rest[0] {
	case 'i', 'm', 's', 'x', '-':
		return true
	default:
		return false
	}
}

func hasWordish(body string) bool {
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' && i+1 < len(body) {
			switch body[i+1] {
			case 'w', 'W', 'b', 'B', 's', 'S':
				return true
			}
			i++
		}
	}
	return false
}

func goPattern(p parsedPat) string {
	var flags string
	if strings.Contains(p.flags, "i") {
		flags += "i"
	}
	if strings.Contains(p.flags, "m") {
		flags += "m"
	}
	if strings.Contains(p.flags, "s") {
		flags += "s"
	}
	if strings.Contains(p.flags, "x") {
		flags += "x"
	}
	if flags == "" {
		return p.body
	}
	return "(?" + flags + ")" + p.body
}

func compileGo(pat string) (*regexp.Regexp, bool) {
	if v, ok := reCache.Load(pat); ok {
		return v.(*regexp.Regexp), true
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return nil, false
	}
	reCache.Store(pat, re)
	return re, true
}

func anchored(flags string) bool { return strings.Contains(flags, "A") }

func findOne(re *regexp.Regexp, subject string, offset int, anchor bool) []int {
	if offset < 0 || offset > len(subject) {
		return nil
	}
	locs := re.FindStringSubmatchIndex(subject[offset:])
	if locs == nil {
		return nil
	}
	if anchor && locs[0] != 0 {
		return nil
	}
	shiftLocs(locs, offset)
	return locs
}

func findAll(re *regexp.Regexp, subject string, offset int, anchor bool) [][]int {
	if anchor {
		one := findOne(re, subject, offset, true)
		if one == nil {
			return nil
		}
		return [][]int{one}
	}
	if offset < 0 || offset > len(subject) {
		return nil
	}
	all := re.FindAllStringSubmatchIndex(subject[offset:], -1)
	for _, locs := range all {
		shiftLocs(locs, offset)
	}
	return all
}

func shiftLocs(locs []int, offset int) {
	for i := range locs {
		if locs[i] >= 0 {
			locs[i] += offset
		}
	}
}

func goMatch(z *zone.Zone, p parsedPat, subject string, flags int64, offset int, ref types.Value, all bool) (types.Value, error, bool) {
	re, ok := compileGo(goPattern(p))
	if !ok {
		return types.Value{}, nil, false
	}
	names := re.SubexpNames()
	offCap := flags&flagOffsetCapture != 0
	nullUn := flags&flagUnmatchedAsNull != 0
	if !all {
		locs := findOne(re, subject, offset, anchored(p.flags))
		if locs == nil {
			empty, err := newArray(z)
			if err != nil {
				return types.Value{}, err, true
			}
			writeRef(z, ref, empty)
			return types.NewLong(0), nil, true
		}
		set, err := matchSet(z, subject, locs, names, offCap, nullUn)
		if err != nil {
			return types.Value{}, err, true
		}
		writeRef(z, ref, set)
		return types.NewLong(1), nil, true
	}
	locs := findAll(re, subject, offset, anchored(p.flags))
	arr, n, err := matchAll(z, subject, locs, names, flags, offCap, nullUn)
	if err != nil {
		return types.Value{}, err, true
	}
	writeRef(z, ref, arr)
	return types.NewLong(int64(n)), nil, true
}

func matchSet(z *zone.Zone, subject string, locs []int, names []string, offCap, nullUn bool) (types.Value, error) {
	a, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	n := len(locs) / 2
	for i := range n {
		cap, err := captureVal(z, subject, locs[2*i], locs[2*i+1], offCap, nullUn)
		if err != nil {
			return types.Value{}, err
		}
		if i > 0 && i < len(names) && names[i] != "" {
			if err = setStrKey(z, &a, names[i], cap); err != nil {
				return types.Value{}, err
			}
		}
		if err = appendVal(z, &a, cap); err != nil {
			return types.Value{}, err
		}
	}
	return a, nil
}

func captureVal(z *zone.Zone, subject string, start, end int, offCap, nullUn bool) (types.Value, error) {
	text := ""
	off := int64(-1)
	unmatched := start < 0
	if !unmatched {
		text = subject[start:end]
		off = int64(start)
	}
	var s types.Value
	var err error
	if unmatched && nullUn {
		s = types.NewNull()
	} else {
		s, err = newStr(z, text)
		if err != nil {
			return types.Value{}, err
		}
	}
	if !offCap {
		return s, nil
	}
	pair, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	if err = appendVal(z, &pair, s); err != nil {
		return types.Value{}, err
	}
	if err = appendVal(z, &pair, types.NewLong(off)); err != nil {
		return types.Value{}, err
	}
	return pair, nil
}

func matchAll(z *zone.Zone, subject string, all [][]int, names []string, flags int64, offCap, nullUn bool) (types.Value, int, error) {
	if flags&flagSetOrder != 0 {
		out, err := newArray(z)
		if err != nil {
			return types.Value{}, 0, err
		}
		for _, locs := range all {
			set, err := matchSet(z, subject, locs, names, offCap, nullUn)
			if err != nil {
				return types.Value{}, 0, err
			}
			if err = appendVal(z, &out, set); err != nil {
				return types.Value{}, 0, err
			}
		}
		return out, len(all), nil
	}
	groups := 1
	if len(all) > 0 {
		groups = len(all[0]) / 2
	} else if len(names) > 0 {
		groups = len(names)
	}
	cols := make([]types.Value, groups)
	for i := range cols {
		a, err := newArray(z)
		if err != nil {
			return types.Value{}, 0, err
		}
		cols[i] = a
	}
	for _, locs := range all {
		n := len(locs) / 2
		for i := 0; i < groups; i++ {
			start, end := -1, -1
			if i < n {
				start, end = locs[2*i], locs[2*i+1]
			}
			cap, err := captureVal(z, subject, start, end, offCap, nullUn)
			if err != nil {
				return types.Value{}, 0, err
			}
			if err = appendVal(z, &cols[i], cap); err != nil {
				return types.Value{}, 0, err
			}
		}
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, 0, err
	}
	for i := 0; i < groups; i++ {
		if i > 0 && i < len(names) && names[i] != "" {
			if err = setStrKey(z, &out, names[i], cols[i]); err != nil {
				return types.Value{}, 0, err
			}
		}
		if err = appendVal(z, &out, cols[i]); err != nil {
			return types.Value{}, 0, err
		}
	}
	return out, len(all), nil
}

func goReplace(z *zone.Zone, p parsedPat, repl, subject string, limit int64, countRef types.Value) (types.Value, error, bool) {
	re, ok := compileGo(goPattern(p))
	if !ok {
		return types.Value{}, nil, false
	}
	if limit == 0 {
		writeRef(z, countRef, types.NewLong(0))
		s, err := newStr(z, subject)
		return s, err, true
	}
	all := findAll(re, subject, 0, anchored(p.flags))
	if limit > 0 && int64(len(all)) > limit {
		all = all[:limit]
	}
	names := re.SubexpNames()
	var b strings.Builder
	pos := 0
	count := 0
	for _, locs := range all {
		if locs[0] < pos {
			continue
		}
		b.WriteString(subject[pos:locs[0]])
		b.WriteString(expandRepl(repl, subject, locs, names))
		pos = locs[1]
		count++
	}
	b.WriteString(subject[pos:])
	writeRef(z, countRef, types.NewLong(int64(count)))
	s, err := newStr(z, b.String())
	return s, err, true
}

func expandRepl(repl, subject string, locs []int, names []string) string {
	nGroups := len(locs) / 2
	var b strings.Builder
	for i := 0; i < len(repl); i++ {
		c := repl[i]
		if c == '\\' && i+1 < len(repl) {
			nxt := repl[i+1]
			if nxt >= '0' && nxt <= '9' {
				j := i + 1
				for j < len(repl) && repl[j] >= '0' && repl[j] <= '9' {
					j++
				}
				idx, _ := strconv.Atoi(repl[i+1 : j])
				b.WriteString(groupText(subject, locs, idx))
				i = j - 1
				continue
			}
			if nxt == '\\' {
				b.WriteByte('\\')
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
				if end >= 0 {
					token := repl[i+2 : i+2+end]
					b.WriteString(groupByToken(subject, locs, names, token))
					i = i + 2 + end
					continue
				}
			}
			if repl[i+1] >= '0' && repl[i+1] <= '9' {
				j := i + 1
				for j < len(repl) && repl[j] >= '0' && repl[j] <= '9' {
					j++
				}
				digits := repl[i+1 : j]
				idx, used := longestGroup(digits, nGroups)
				if used == 0 {
					b.WriteByte('$')
					continue
				}
				b.WriteString(groupText(subject, locs, idx))
				i += used
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

func longestGroup(digits string, nGroups int) (int, int) {
	for end := len(digits); end > 0; end-- {
		n, err := strconv.Atoi(digits[:end])
		if err != nil {
			continue
		}
		if n < nGroups {
			return n, end
		}
	}
	return 0, 0
}

func groupByToken(subject string, locs []int, names []string, token string) string {
	if n, err := strconv.Atoi(token); err == nil {
		return groupText(subject, locs, n)
	}
	for i, name := range names {
		if name == token {
			return groupText(subject, locs, i)
		}
	}
	return ""
}

func groupText(subject string, locs []int, idx int) string {
	if idx < 0 || idx >= len(locs)/2 {
		return ""
	}
	start, end := locs[2*idx], locs[2*idx+1]
	if start < 0 || end < start || end > len(subject) {
		return ""
	}
	return subject[start:end]
}

func goSplit(z *zone.Zone, p parsedPat, subject string, limit, flags int64) (types.Value, error, bool) {
	re, ok := compileGo(goPattern(p))
	if !ok {
		return types.Value{}, nil, false
	}
	all := findAll(re, subject, 0, anchored(p.flags))
	noEmpty := flags&flagSplitNoEmpty != 0
	delim := flags&flagSplitDelim != 0
	offCap := flags&flagSplitOffset != 0
	maxPieces := -1
	if limit > 0 {
		maxPieces = int(limit)
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err, true
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
		if err = emitSplit(z, &out, subject, pos, locs[0], noEmpty, offCap); err != nil {
			return types.Value{}, err, true
		}
		emitted++
		if delim {
			for g := 1; g < len(locs)/2; g++ {
				start, end := locs[2*g], locs[2*g+1]
				if start < 0 {
					continue
				}
				if err = emitSplit(z, &out, subject, start, end, noEmpty, offCap); err != nil {
					return types.Value{}, err, true
				}
			}
		}
		pos = locs[1]
	}
	if err = emitSplit(z, &out, subject, pos, len(subject), noEmpty, offCap); err != nil {
		return types.Value{}, err, true
	}
	return out, nil, true
}

func emitSplit(z *zone.Zone, out *types.Value, subject string, start, end int, noEmpty, offCap bool) error {
	if start < 0 || end < start || end > len(subject) {
		return nil
	}
	text := subject[start:end]
	if noEmpty && text == "" {
		return nil
	}
	s, err := newStr(z, text)
	if err != nil {
		return err
	}
	if !offCap {
		return appendVal(z, out, s)
	}
	pair, err := newArray(z)
	if err != nil {
		return err
	}
	if err = appendVal(z, &pair, s); err != nil {
		return err
	}
	if err = appendVal(z, &pair, types.NewLong(int64(start))); err != nil {
		return err
	}
	return appendVal(z, out, pair)
}

func quotePattern(s, delim string) string {
	const special = ".\\+*?[^]$(){}=!<>|:-#"
	var extra byte
	if delim != "" {
		extra = delim[0]
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(special, c) >= 0 || (extra != 0 && c == extra && strings.IndexByte(special, c) < 0) {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}

func grepASCII(z *zone.Zone, arr types.Value, p parsedPat) bool {
	ok := true
	types.ArrayForEach(z, arr, func(_, v types.Value) bool {
		v = deref(z, v)
		if v.Type != types.TypeString {
			return true
		}
		s := types.StringData(z, v)
		if !re2Safe(p, s, 0) {
			ok = false
			return false
		}
		return true
	})
	return ok
}

func goGrep(z *zone.Zone, p parsedPat, arr types.Value, flags int64) (types.Value, error, bool) {
	re, ok := compileGo(goPattern(p))
	if !ok {
		return types.Value{}, nil, false
	}
	invert := flags&flagGrepInvert != 0
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err, true
	}
	var walkErr error
	types.ArrayForEach(z, arr, func(k, v types.Value) bool {
		s, err := asString(z, v, "preg_grep", "2", "array")
		if err != nil {
			walkErr = err
			return false
		}
		matched := re.MatchString(s)
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
		return types.Value{}, walkErr, true
	}
	return out, nil, true
}
