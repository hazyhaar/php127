package ext

import (
	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/types"
)

type nativeEntry struct {
	name string
	fn   stdlib.NativeFunc
}

func Lookup(name string) (stdlib.NativeFunc, bool) {
	lo, hi := 0, len(natives)
	for lo < hi {
		mid := (lo + hi) >> 1
		c := cmpFold(name, natives[mid].name)
		if c == 0 {
			return natives[mid].fn, true
		}
		if c < 0 {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return nil, false
}

func cmpFold(name, canon string) int {
	n, m := len(name), len(canon)
	lim := min(m, n)
	for i := range lim {
		a := name[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		b := canon[i]
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
	}
	if n < m {
		return -1
	}
	if n > m {
		return 1
	}
	return 0
}

func Constants() map[string]types.Value { return constants }

var constants map[string]types.Value

func init() {
	for i := 1; i < len(natives); i++ {
		if c := cmpFold(natives[i-1].name, natives[i].name); c >= 0 {
			panic("ext: natives not sorted: " + natives[i-1].name + " >= " + natives[i].name)
		}
	}
	constants = map[string]types.Value{
		"PREG_PATTERN_ORDER":        types.NewLong(1),
		"PREG_SET_ORDER":            types.NewLong(2),
		"PREG_OFFSET_CAPTURE":       types.NewLong(256),
		"PREG_UNMATCHED_AS_NULL":    types.NewLong(512),
		"PREG_SPLIT_NO_EMPTY":       types.NewLong(1),
		"PREG_SPLIT_DELIM_CAPTURE":  types.NewLong(2),
		"PREG_SPLIT_OFFSET_CAPTURE": types.NewLong(4),
		"PREG_GREP_INVERT":          types.NewLong(1),
	}
	for name, n := range longConstants {
		constants[name] = types.NewLong(n)
	}
	for name, n := range gdLongConstants {
		constants[name] = types.NewLong(n)
	}
	for name, n := range curlXMLLongConstants {
		constants[name] = types.NewLong(n)
	}
	for name, text := range dateFormats {
		v, err := types.InternProcessString(text)
		if err != nil {
			panic(err)
		}
		constants[name] = v
	}
}

var longConstants = map[string]int64{
	"FORCE_GZIP":                          zlibEncodingGzip,
	"FORCE_DEFLATE":                       zlibEncodingDeflate,
	"ZLIB_ENCODING_RAW":                   zlibEncodingRaw,
	"ZLIB_ENCODING_GZIP":                  zlibEncodingGzip,
	"ZLIB_ENCODING_DEFLATE":               zlibEncodingDeflate,
	"MB_CASE_UPPER":                       mbCaseUpper,
	"MB_CASE_LOWER":                       mbCaseLower,
	"MB_CASE_TITLE":                       mbCaseTitle,
	"MB_CASE_FOLD":                        mbCaseFold,
	"MB_CASE_UPPER_SIMPLE":                mbCaseUpperSimple,
	"MB_CASE_LOWER_SIMPLE":                mbCaseLowerSimple,
	"MB_CASE_TITLE_SIMPLE":                mbCaseTitleSimple,
	"MB_CASE_FOLD_SIMPLE":                 mbCaseFoldSimple,
	"PREG_NO_ERROR":                       0,
	"PREG_INTERNAL_ERROR":                 1,
	"PREG_BACKTRACK_LIMIT_ERROR":          2,
	"PREG_RECURSION_LIMIT_ERROR":          3,
	"PREG_BAD_UTF8_ERROR":                 4,
	"PREG_BAD_UTF8_OFFSET_ERROR":          5,
	"PREG_JIT_STACKLIMIT_ERROR":           6,
	"OPENSSL_RAW_DATA":                    opensslRawData,
	"OPENSSL_ZERO_PADDING":                opensslZeroPadding,
	"OPENSSL_DONT_ZERO_PAD_KEY":           opensslDontZeroPadKey,
	"ICONV_MIME_DECODE_STRICT":            1,
	"ICONV_MIME_DECODE_CONTINUE_ON_ERROR": iconvMimeContinueOnError,
	"HASH_HMAC":                           hashHMACFlag,
	"OPENSSL_ALGO_SHA1":                   opensslAlgoSHA1,
	"OPENSSL_ALGO_MD5":                    opensslAlgoMD5,
	"OPENSSL_ALGO_MD4":                    opensslAlgoMD4,
	"OPENSSL_ALGO_SHA224":                 opensslAlgoSHA224,
	"OPENSSL_ALGO_SHA256":                 opensslAlgoSHA256,
	"OPENSSL_ALGO_SHA384":                 opensslAlgoSHA384,
	"OPENSSL_ALGO_SHA512":                 opensslAlgoSHA512,
	"OPENSSL_ALGO_RMD160":                 opensslAlgoRMD160,
	"IDNA_DEFAULT":                        idnaDefault,
	"IDNA_ALLOW_UNASSIGNED":               idnaAllowUnassigned,
	"IDNA_USE_STD3_RULES":                 idnaUseSTD3Rules,
	"IDNA_CHECK_BIDI":                     idnaCheckBidi,
	"IDNA_CHECK_CONTEXTJ":                 idnaCheckContextJ,
	"IDNA_NONTRANSITIONAL_TO_ASCII":       idnaNontransitionalToASCII,
	"IDNA_NONTRANSITIONAL_TO_UNICODE":     idnaNontransitionalToUni,
	"INTL_IDNA_VARIANT_UTS46":             intlIDNAVariantUTS46,
	"IDNA_ERROR_EMPTY_LABEL":              idnaErrEmptyLabel,
	"IDNA_ERROR_LABEL_TOO_LONG":           idnaErrLabelTooLong,
	"IDNA_ERROR_DOMAIN_NAME_TOO_LONG":     idnaErrDomainTooLong,
	"IDNA_ERROR_LEADING_HYPHEN":           idnaErrLeadingHyphen,
	"IDNA_ERROR_TRAILING_HYPHEN":          idnaErrTrailingHyphen,
	"IDNA_ERROR_HYPHEN_3_4":               idnaErrHyphen34,
	"IDNA_ERROR_LEADING_COMBINING_MARK":   idnaErrLeadingMark,
	"IDNA_ERROR_DISALLOWED":               idnaErrDisallowed,
	"IDNA_ERROR_PUNYCODE":                 idnaErrPunycode,
	"IDNA_ERROR_INVALID_ACE_LABEL":        idnaErrInvalidACE,
	"IDNA_ERROR_BIDI":                     idnaErrBidi,
	"IDNA_ERROR_CONTEXTJ":                 idnaErrContextJ,
}

var dateFormats = map[string]string{
	"DATE_ATOM":             `Y-m-d\TH:i:sP`,
	"DATE_COOKIE":           "l, d-M-Y H:i:s T",
	"DATE_ISO8601":          `Y-m-d\TH:i:sO`,
	"DATE_RFC822":           "D, d M y H:i:s O",
	"DATE_RFC850":           "l, d-M-y H:i:s T",
	"DATE_RFC1036":          "D, d M y H:i:s O",
	"DATE_RFC1123":          "D, d M Y H:i:s O",
	"DATE_RFC2822":          "D, d M Y H:i:s O",
	"DATE_RFC3339":          `Y-m-d\TH:i:sP`,
	"DATE_RFC3339_EXTENDED": `Y-m-d\TH:i:s.vP`,
	"DATE_RFC7231":          `D, d M Y H:i:s \G\M\T`,
	"DATE_RSS":              "D, d M Y H:i:s O",
	"DATE_W3C":              `Y-m-d\TH:i:sP`,
}

var natives = [...]nativeEntry{
	{"base64_decode", nativeBase64Decode},
	{"base64_encode", nativeBase64Encode},
	{"crc32", nativeCRC32},
	{"ctype_print", nativeCtypePrint},
	{"curl_close", nativeCurlClose},
	{"curl_errno", nativeCurlErrno},
	{"curl_error", nativeCurlError},
	{"curl_exec", nativeCurlExec},
	{"curl_getinfo", nativeCurlGetinfo},
	{"curl_init", nativeCurlInit},
	{"curl_reset", nativeCurlReset},
	{"curl_setopt", nativeCurlSetopt},
	{"curl_setopt_array", nativeCurlSetoptArray},
	{"curl_version", nativeCurlVersion},
	{"date", nativeDate},
	{"date_parse_from_format", nativeDateParseFromFormat},
	{"finfo_buffer", nativeFinfoBuffer},
	{"finfo_close", nativeFinfoClose},
	{"finfo_file", nativeFinfoFile},
	{"finfo_open", nativeFinfoOpen},
	{"gd_info", nativeGDInfo},
	{"gmdate", nativeGMDate},
	{"gmmktime", nativeGMMktime},
	{"gmstrftime", nativeGMStrftime},
	{"gzcompress", nativeGzcompress},
	{"gzdecode", nativeGzdecode},
	{"gzdeflate", nativeGzdeflate},
	{"gzencode", nativeGzencode},
	{"gzinflate", nativeGzinflate},
	{"gzuncompress", nativeGzuncompress},
	{"hash", nativeHash},
	{"hash_algos", nativeHashAlgos},
	{"hash_copy", nativeHashCopy},
	{"hash_equals", nativeHashEquals},
	{"hash_file", nativeHashFile},
	{"hash_final", nativeHashFinal},
	{"hash_hkdf", nativeHashHKDF},
	{"hash_hmac", nativeHashHMAC},
	{"hash_hmac_algos", nativeHashHMACAlgos},
	{"hash_hmac_file", nativeHashHMACFile},
	{"hash_init", nativeHashInit},
	{"hash_pbkdf2", nativeHashPBKDF2},
	{"hash_update", nativeHashUpdate},
	{"hrtime", nativeHrtime},
	{"iconv", nativeIconv},
	{"iconv_get_encoding", nativeIconvGetEncoding},
	{"iconv_mime_decode", nativeIconvMimeDecode},
	{"iconv_set_encoding", nativeIconvSetEncoding},
	{"iconv_strlen", nativeIconvStrlen},
	{"iconv_strpos", nativeIconvStrpos},
	{"iconv_strrpos", nativeIconvStrrpos},
	{"iconv_substr", nativeIconvSubstr},
	{"idn_to_ascii", nativeIdnToASCII},
	{"imagealphablending", nativeImagealphablending},
	{"imageantialias", nativeImageantialias},
	{"imagecolorallocate", nativeImagecolorallocate},
	{"imagecolorallocatealpha", nativeImagecolorallocatealpha},
	{"imagecolorat", nativeImagecolorat},
	{"imagecolorsforindex", nativeImagecolorsforindex},
	{"imagecolorstotal", nativeImagecolorstotal},
	{"imagecolortransparent", nativeImagecolortransparent},
	{"imagecopy", nativeImagecopy},
	{"imagecopyresampled", nativeImagecopyresampled},
	{"imagecopyresized", nativeImagecopyresized},
	{"imagecreate", nativeImagecreate},
	{"imagecreatefromgif", nativeImagecreatefromgif},
	{"imagecreatefromjpeg", nativeImagecreatefromjpeg},
	{"imagecreatefrompng", nativeImagecreatefrompng},
	{"imagecreatefromstring", nativeImagecreatefromstring},
	{"imagecreatetruecolor", nativeImagecreatetruecolor},
	{"imagedestroy", nativeImagedestroy},
	{"imagefill", nativeImagefill},
	{"imagefilledrectangle", nativeImagefilledrectangle},
	{"imageflip", nativeImageflip},
	{"imagegif", nativeImagegif},
	{"imageinterlace", nativeImageinterlace},
	{"imageistruecolor", nativeImageistruecolor},
	{"imagejpeg", nativeImagejpeg},
	{"imagepng", nativeImagepng},
	{"imagerotate", nativeImagerotate},
	{"imagesavealpha", nativeImagesavealpha},
	{"imagesetpixel", nativeImagesetpixel},
	{"imagesx", nativeImagesx},
	{"imagesy", nativeImagesy},
	{"imagetruecolortopalette", nativeImagetruecolortopalette},
	{"imagetypes", nativeImagetypes},
	{"mb_check_encoding", nativeMBCheckEncoding},
	{"mb_convert_case", nativeMBConvertCase},
	{"mb_convert_encoding", nativeMBConvertEncoding},
	{"mb_convert_variables", nativeMBConvertVariables},
	{"mb_decode_mimeheader", nativeMBDecodeMimeheader},
	{"mb_detect_encoding", nativeMBDetectEncoding},
	{"mb_detect_order", nativeMBDetectOrder},
	{"mb_internal_encoding", nativeMBInternalEncoding},
	{"mb_language", nativeMBLanguage},
	{"mb_list_encodings", nativeMBListEncodings},
	{"mb_regex_encoding", nativeMBRegexEncoding},
	{"mb_scrub", nativeMBScrub},
	{"mb_strcut", nativeMBStrcut},
	{"mb_strimwidth", nativeMBStrimwidth},
	{"mb_stripos", nativeMBStripos},
	{"mb_stristr", nativeMBStristr},
	{"mb_strlen", nativeMBStrlen},
	{"mb_strpos", nativeMBStrpos},
	{"mb_strrchr", nativeMBStrrchr},
	{"mb_strrichr", nativeMBStrrichr},
	{"mb_strripos", nativeMBStrripos},
	{"mb_strrpos", nativeMBStrrpos},
	{"mb_strstr", nativeMBStrstr},
	{"mb_strtolower", nativeMBStrtolower},
	{"mb_strtoupper", nativeMBStrtoupper},
	{"mb_strwidth", nativeMBStrwidth},
	{"mb_substitute_character", nativeMBSubstituteCharacter},
	{"mb_substr", nativeMBSubstr},
	{"mb_substr_count", nativeMBSubstrCount},
	{"md5", nativeMD5},
	{"md5_file", nativeMD5File},
	{"microtime", nativeMicrotime},
	{"mime_content_type", nativeMimeContentType},
	{"mktime", nativeMktime},
	{"normalizer_is_normalized", nativeNormalizerIsNormalized},
	{"normalizer_normalize", nativeNormalizerNormalize},
	{"openssl_cipher_iv_length", nativeOpensslCipherIVLength},
	{"openssl_decrypt", nativeOpensslDecrypt},
	{"openssl_encrypt", nativeOpensslEncrypt},
	{"openssl_error_string", nativeOpensslErrorString},
	{"openssl_get_cipher_methods", nativeOpensslGetCipherMethods},
	{"openssl_pkey_free", nativeOpensslPkeyFree},
	{"openssl_pkey_get_private", nativeOpensslPkeyGetPrivate},
	{"openssl_random_pseudo_bytes", nativeOpensslRandomPseudoBytes},
	{"openssl_sign", nativeOpensslSign},
	{"openssl_x509_parse", nativeOpensslX509Parse},
	{"password_needs_rehash", nativePasswordNeedsRehash},
	{"preg_grep", nativePregGrep},
	{"preg_last_error", nativePregLastError},
	{"preg_last_error_msg", nativePregLastErrorMsg},
	{"preg_match", nativePregMatch},
	{"preg_match_all", nativePregMatchAll},
	{"preg_quote", nativePregQuote},
	{"preg_replace", nativePregReplace},
	{"preg_split", nativePregSplit},
	{"rawurldecode", nativeRawURLDecode},
	{"rawurlencode", nativeRawURLEncode},
	{"sha1", nativeSHA1},
	{"sha1_file", nativeSHA1File},
	{"strftime", nativeStrftime},
	{"strtotime", nativeStrtotime},
	{"time", nativeTime},
	{"timezone_location_get", nativeTimezoneLocationGet},
	{"timezone_transitions_get", nativeTimezoneTransitionsGet},
	{"urldecode", nativeURLDecode},
	{"urlencode", nativeURLEncode},
	{"xml_error_string", nativeXMLErrorString},
	{"xml_get_current_byte_index", nativeXMLGetCurrentByteIndex},
	{"xml_get_current_column_number", nativeXMLGetCurrentColumnNumber},
	{"xml_get_current_line_number", nativeXMLGetCurrentLineNumber},
	{"xml_get_error_code", nativeXMLGetErrorCode},
	{"xml_parse", nativeXMLParse},
	{"xml_parse_into_struct", nativeXMLParseIntoStruct},
	{"xml_parser_create", nativeXMLParserCreate},
	{"xml_parser_create_ns", nativeXMLParserCreateNS},
	{"xml_parser_free", nativeXMLParserFree},
	{"xml_parser_get_option", nativeXMLParserGetOption},
	{"xml_parser_set_option", nativeXMLParserSetOption},
	{"xml_set_character_data_handler", nativeXMLSetCharacterDataHandler},
	{"xml_set_default_handler", nativeXMLSetDefaultHandler},
	{"xml_set_element_handler", nativeXMLSetElementHandler},
	{"xml_set_end_namespace_decl_handler", nativeXMLSetEndNamespaceDeclHandler},
	{"xml_set_external_entity_ref_handler", nativeXMLSetExternalEntityRefHandler},
	{"xml_set_notation_decl_handler", nativeXMLSetNotationDeclHandler},
	{"xml_set_object", nativeXMLSetObject},
	{"xml_set_processing_instruction_handler", nativeXMLSetProcessingInstructionHandler},
	{"xml_set_start_namespace_decl_handler", nativeXMLSetStartNamespaceDeclHandler},
	{"xml_set_unparsed_entity_decl_handler", nativeXMLSetUnparsedEntityDeclHandler},
	{"zlib_decode", nativeZlibDecode},
	{"zlib_encode", nativeZlibEncode},
}
