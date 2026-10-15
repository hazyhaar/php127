package sapi

import (
	"strings"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// CLIServer construit le $_SERVER de la SAPI CLI de PHP : variables d'environnement env (forme CLÉ=valeur),
// puis PHP_SELF, SCRIPT_NAME, SCRIPT_FILENAME, PATH_TRANSLATED, DOCUMENT_ROOT, REQUEST_TIME_FLOAT,
// REQUEST_TIME, argv et argc. argv[0] est le script tel que nommé sur la ligne de commande ; un code
// fourni par -r ou l'entrée standard porte PHP_SELF « Standard input code » et un SCRIPT_FILENAME vide.
func CLIServer(z *zone.Zone, argv []string, env []string, inline bool, now time.Time) (types.Value, error) {
	srv, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	fail := func(err error) (types.Value, error) {
		types.ReleaseValue(z, &srv)
		return types.Value{}, err
	}
	setStr := func(k, v string) error {
		sv, err := newStr(z, v)
		if err != nil {
			return err
		}
		defer types.ReleaseValue(z, &sv)
		return setStrKey(z, &srv, k, sv)
	}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		if err := setStr(k, v); err != nil {
			return fail(err)
		}
	}
	self, file := "", ""
	if len(argv) > 0 {
		self, file = argv[0], argv[0]
	}
	if inline {
		self, file = "Standard input code", ""
	}
	for _, kv := range [][2]string{{"PHP_SELF", self}, {"SCRIPT_NAME", self}, {"SCRIPT_FILENAME", file}, {"PATH_TRANSLATED", file}, {"DOCUMENT_ROOT", ""}} {
		if err := setStr(kv[0], kv[1]); err != nil {
			return fail(err)
		}
	}
	if err := setStrKey(z, &srv, "REQUEST_TIME_FLOAT", types.NewDouble(float64(now.UnixMicro())/1e6)); err != nil {
		return fail(err)
	}
	if err := setStrKey(z, &srv, "REQUEST_TIME", types.NewLong(now.Unix())); err != nil {
		return fail(err)
	}
	args, err := types.NewArray(z)
	if err != nil {
		return fail(err)
	}
	for _, a := range argv {
		sv, err := newStr(z, a)
		if err == nil {
			err = types.ArrayAppend(z, &args, sv)
			types.ReleaseValue(z, &sv)
		}
		if err != nil {
			types.ReleaseValue(z, &args)
			return fail(err)
		}
	}
	err = setStrKey(z, &srv, "argv", args)
	types.ReleaseValue(z, &args)
	if err != nil {
		return fail(err)
	}
	if err := setStrKey(z, &srv, "argc", types.NewLong(int64(len(argv)))); err != nil {
		return fail(err)
	}
	return srv, nil
}
