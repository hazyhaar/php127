package bytecode

import "strings"

// NativeRefSpec décrit la signature par référence d'une fonction ou d'une méthode native : noms des paramètres
// dans l'ordre de PHP, masque des paramètres transmis par référence (bit i pour le paramètre i) et indice à
// partir duquel tout argument est transmis par référence (paramètre variadique par référence), -1 sinon.
type NativeRefSpec struct {
	Params       []string
	RefMask      uint64
	VariadicFrom int
}

// nativeFuncRefs est la table unique des fonctions natives qui reçoivent au moins un argument par référence,
// indexée par nom en minuscules. Le compilateur en tire la transmission des arguments d'un appel nommé, la VM
// le passage des références aux fonctions natives et aux appels dont la cible n'est connue qu'à l'exécution.
var nativeFuncRefs = map[string]NativeRefSpec{
	"preg_match":                  {Params: []string{"pattern", "subject", "matches", "flags", "offset"}, RefMask: 1 << 2, VariadicFrom: -1},
	"preg_match_all":              {Params: []string{"pattern", "subject", "matches", "flags", "offset"}, RefMask: 1 << 2, VariadicFrom: -1},
	"preg_replace":                {Params: []string{"pattern", "replacement", "subject", "limit", "count"}, RefMask: 1 << 4, VariadicFrom: -1},
	"preg_replace_callback":       {Params: []string{"pattern", "replacement", "subject", "limit", "count"}, RefMask: 1 << 4, VariadicFrom: -1},
	"str_replace":                 {Params: []string{"search", "replace", "subject", "count"}, RefMask: 1 << 3, VariadicFrom: -1},
	"str_ireplace":                {Params: []string{"search", "replace", "subject", "count"}, RefMask: 1 << 3, VariadicFrom: -1},
	"parse_str":                   {Params: []string{"string", "result"}, RefMask: 1 << 1, VariadicFrom: -1},
	"headers_sent":                {Params: []string{"filename", "line"}, RefMask: 1<<0 | 1<<1, VariadicFrom: -1},
	"sort":                        {Params: []string{"array", "flags"}, RefMask: 1 << 0, VariadicFrom: -1},
	"rsort":                       {Params: []string{"array", "flags"}, RefMask: 1 << 0, VariadicFrom: -1},
	"asort":                       {Params: []string{"array", "flags"}, RefMask: 1 << 0, VariadicFrom: -1},
	"arsort":                      {Params: []string{"array", "flags"}, RefMask: 1 << 0, VariadicFrom: -1},
	"ksort":                       {Params: []string{"array", "flags"}, RefMask: 1 << 0, VariadicFrom: -1},
	"krsort":                      {Params: []string{"array", "flags"}, RefMask: 1 << 0, VariadicFrom: -1},
	"array_push":                  {Params: []string{"array", "values"}, RefMask: 1 << 0, VariadicFrom: -1},
	"array_pop":                   {Params: []string{"array"}, RefMask: 1 << 0, VariadicFrom: -1},
	"array_shift":                 {Params: []string{"array"}, RefMask: 1 << 0, VariadicFrom: -1},
	"array_unshift":               {Params: []string{"array", "values"}, RefMask: 1 << 0, VariadicFrom: -1},
	"array_splice":                {Params: []string{"array", "offset", "length", "replacement"}, RefMask: 1 << 0, VariadicFrom: -1},
	"next":                        {Params: []string{"array"}, RefMask: 1 << 0, VariadicFrom: -1},
	"prev":                        {Params: []string{"array"}, RefMask: 1 << 0, VariadicFrom: -1},
	"reset":                       {Params: []string{"array"}, RefMask: 1 << 0, VariadicFrom: -1},
	"end":                         {Params: []string{"array"}, RefMask: 1 << 0, VariadicFrom: -1},
	"natsort":                     {Params: []string{"array", "flags"}, RefMask: 1 << 0, VariadicFrom: -1},
	"natcasesort":                 {Params: []string{"array", "flags"}, RefMask: 1 << 0, VariadicFrom: -1},
	"shuffle":                     {Params: []string{"array", "flags"}, RefMask: 1 << 0, VariadicFrom: -1},
	"settype":                     {Params: []string{"var", "type"}, RefMask: 1 << 0, VariadicFrom: -1},
	"usort":                       {Params: []string{"array", "callback", "arg"}, RefMask: 1 << 0, VariadicFrom: -1},
	"uasort":                      {Params: []string{"array", "callback", "arg"}, RefMask: 1 << 0, VariadicFrom: -1},
	"uksort":                      {Params: []string{"array", "callback", "arg"}, RefMask: 1 << 0, VariadicFrom: -1},
	"array_walk":                  {Params: []string{"array", "callback", "arg"}, RefMask: 1 << 0, VariadicFrom: -1},
	"array_walk_recursive":        {Params: []string{"array", "callback", "arg"}, RefMask: 1 << 0, VariadicFrom: -1},
	"array_multisort":             {Params: []string{"array", "rest"}, RefMask: 1 << 0, VariadicFrom: 1},
	"similar_text":                {Params: []string{"string1", "string2", "percent"}, RefMask: 1 << 2, VariadicFrom: -1},
	"sscanf":                      {Params: []string{"string", "format", "vars"}, RefMask: 0, VariadicFrom: 2},
	"exec":                        {Params: []string{"command", "output", "result_code"}, RefMask: 1<<1 | 1<<2, VariadicFrom: -1},
	"fsockopen":                   {Params: []string{"hostname", "port", "error_code", "error_message", "timeout"}, RefMask: 1<<2 | 1<<3, VariadicFrom: -1},
	"pfsockopen":                  {Params: []string{"hostname", "port", "error_code", "error_message", "timeout"}, RefMask: 1<<2 | 1<<3, VariadicFrom: -1},
	"stream_select":               {Params: []string{"read", "write", "except", "seconds", "microseconds"}, RefMask: 1<<0 | 1<<1 | 1<<2, VariadicFrom: -1},
	"openssl_sign":                {Params: []string{"data", "signature", "private_key", "algorithm"}, RefMask: 1 << 1, VariadicFrom: -1},
	"stream_socket_client":        {Params: []string{"address", "error_code", "error_message", "timeout", "flags", "context"}, RefMask: 1<<1 | 1<<2, VariadicFrom: -1},
	"flock":                       {Params: []string{"stream", "operation", "would_block"}, RefMask: 1 << 2, VariadicFrom: -1},
	"getimagesize":                {Params: []string{"filename", "image_info"}, RefMask: 1 << 1, VariadicFrom: -1},
	"getimagesizefromstring":      {Params: []string{"string", "image_info"}, RefMask: 1 << 1, VariadicFrom: -1},
	"openssl_random_pseudo_bytes": {Params: []string{"length", "strong_result"}, RefMask: 1 << 1, VariadicFrom: -1},
	"openssl_encrypt":             {Params: []string{"data", "cipher_algo", "passphrase", "options", "iv", "tag", "aad", "tag_length"}, RefMask: 1 << 5, VariadicFrom: -1},
	"mb_convert_variables":        {Params: []string{"to_encoding", "from_encoding", "var", "vars"}, RefMask: 1 << 2, VariadicFrom: 2},
	"idn_to_ascii":                {Params: []string{"domain", "flags", "variant", "idna_info"}, RefMask: 1 << 3, VariadicFrom: -1},
	"mysqli_stmt_bind_param":      {Params: []string{"statement", "types", "vars"}, RefMask: 0, VariadicFrom: 2},
	"mysqli_stmt_bind_result":     {Params: []string{"statement", "vars"}, RefMask: 0, VariadicFrom: 1},
	"xml_parse_into_struct":       {Params: []string{"parser", "data", "values", "index"}, RefMask: 1<<2 | 1<<3, VariadicFrom: -1},
}

// nativeMethodRefs recense, par nom de méthode en minuscules, les méthodes natives qui reçoivent un argument par
// référence ; la classe de l'objet n'étant pas connue à la compilation, le nom seul décide.
var nativeMethodRefs = map[string]NativeRefSpec{
	"bindparam":   {Params: []string{"param", "var", "type", "maxLength", "driverOptions"}, RefMask: 1 << 1, VariadicFrom: -1},
	"bindcolumn":  {Params: []string{"column", "var", "type", "maxLength", "driverOptions"}, RefMask: 1 << 1, VariadicFrom: -1},
	"bind_param":  {Params: []string{"types", "vars"}, RefMask: 0, VariadicFrom: 1},
	"bind_result": {Params: []string{"vars"}, RefMask: 0, VariadicFrom: 0},
}

func nativeFuncSpec(name string) (NativeRefSpec, bool) {
	if s, ok := nativeFuncRefs[name]; ok {
		return s, true
	}
	s, ok := nativeFuncRefs[strings.ToLower(strings.TrimPrefix(name, `\`))]
	return s, ok
}

func nativeMethodSpec(method string) (NativeRefSpec, bool) {
	if s, ok := nativeMethodRefs[method]; ok {
		return s, true
	}
	s, ok := nativeMethodRefs[strings.ToLower(method)]
	return s, ok
}

// ArgIsRef indique si l'argument d'indice idx se transmet par référence.
func (s NativeRefSpec) ArgIsRef(idx int) bool {
	if idx < 0 {
		return false
	}
	if s.VariadicFrom >= 0 && idx >= s.VariadicFrom {
		return true
	}
	return idx < 64 && s.RefMask&(1<<uint(idx)) != 0
}

// ParamInfos rend la signature sous la forme attendue par le compilateur : le dernier paramètre est variadique
// lorsque VariadicFrom le désigne.
func (s NativeRefSpec) ParamInfos() []ParamInfo {
	out := make([]ParamInfo, len(s.Params))
	for i, name := range s.Params {
		out[i] = ParamInfo{Name: name, ByRef: s.ArgIsRef(i), Variadic: s.VariadicFrom >= 0 && i == len(s.Params)-1 && i >= s.VariadicFrom}
	}
	return out
}

// NativeFunctionParams rend la signature d'une fonction native à arguments par référence, nil pour une fonction absente de la table.
func NativeFunctionParams(name string) []ParamInfo {
	s, ok := nativeFuncSpec(name)
	if !ok {
		return nil
	}
	return s.ParamInfos()
}

// NativeHasRefArgs indique qu'une fonction native reçoit au moins un argument par référence.
func NativeHasRefArgs(name string) bool {
	_, ok := nativeFuncSpec(name)
	return ok
}

// NativeArgIsRef indique si l'argument d'indice idx d'une fonction native se transmet par référence.
func NativeArgIsRef(name string, idx int) bool {
	s, ok := nativeFuncSpec(name)
	return ok && s.ArgIsRef(idx)
}

// NativeMethodParams rend la signature d'une méthode native à arguments par référence.
func NativeMethodParams(method string) ([]ParamInfo, bool) {
	s, ok := nativeMethodSpec(method)
	if !ok {
		return nil, false
	}
	return s.ParamInfos(), true
}

// NativeMethodArgIsRef indique si l'argument d'indice idx d'une méthode native se transmet par référence.
func NativeMethodArgIsRef(method string, idx int) bool {
	s, ok := nativeMethodSpec(method)
	return ok && s.ArgIsRef(idx)
}
