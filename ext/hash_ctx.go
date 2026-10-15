package ext

import (
	"crypto/hmac"
	"encoding/hex"
	"hash"
	"io"

	"code.hazyhaar.fr/devhoros/php127/streams"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// Sortes de ressource des handles d'ext, distinctes de celles de streams (1 à 3). PHP 8 rend des objets
// HashContext et OpenSSLAsymmetricKey ; sans classe native déclarée par la VM, le handle est une ressource
// dont l'état vit, hors de l'arène, dans l'état d'ext de la Zone.
const (
	resourceKindHashContext uint16 = 0x4501
	resourceKindPKey        uint16 = 0x4502
)

// hashHMACFlag est la valeur de la constante HASH_HMAC.
const hashHMACFlag = 1

// hashCtx est un contexte de hachage incrémental ; hash.Cloner porte hash_copy sans journal des octets.
type hashCtx struct {
	h hash.Hash
}

// newHandle attribue le prochain identifiant de handle de la Zone.
func (st *extState) newHandle() uint32 {
	st.nextHandle++
	return st.nextHandle
}

func storeHashCtx(z *zone.Zone, ctx *hashCtx) types.Value {
	st := extStateFor(z)
	if st.hashCtxs == nil {
		st.hashCtxs = make(map[uint32]*hashCtx)
	}
	id := st.newHandle()
	st.hashCtxs[id] = ctx
	return streams.NewResource(z, resourceKindHashContext, id)
}

// hashCtxArg résout le contexte désigné par l'argument #1 ; un contexte finalisé n'existe plus.
func hashCtxArg(z *zone.Zone, v types.Value, fn string) (uint32, *hashCtx, error) {
	v = deref(z, v)
	if v.Type != types.TypeResource || v.Extra != resourceKindHashContext {
		got := typeKind(z, v)
		if v.Type == types.TypeResource {
			got = "resource"
		}
		return 0, nil, typeErr(fn, "1", "context", "HashContext", got)
	}
	ctx := extStateFor(z).hashCtxs[v.DataSlot]
	if ctx == nil {
		return 0, nil, TypeError{fn + "(): Argument #1 ($context) must be a valid, non-finalized HashContext"}
	}
	return v.DataSlot, ctx, nil
}

func nativeHashInit(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hash_init", len(args), 1, 4); err != nil {
		return types.Value{}, err
	}
	name, err := asString(z, args[0], "hash_init", "1", "algo")
	if err != nil {
		return types.Value{}, err
	}
	flags, err := optInt(z, args, 1, 0, "hash_init", "2", "flags")
	if err != nil {
		return types.Value{}, err
	}
	key, err := optString(z, args, 2, "hash_init", "3", "key")
	if err != nil {
		return types.Value{}, err
	}
	a, ok := findAlgo(name)
	if !ok {
		return types.Value{}, ValueError{"hash_init(): Argument #1 ($algo) must be a valid hashing algorithm"}
	}
	if flags&hashHMACFlag == 0 {
		return storeHashCtx(z, &hashCtx{h: a.new()}), nil
	}
	if !a.hmac {
		return types.Value{}, ValueError{"hash_init(): Argument #1 ($algo) must be a cryptographic hashing algorithm if HMAC is requested"}
	}
	if key == "" {
		return types.Value{}, ValueError{"hash_init(): Argument #3 ($key) cannot be empty when HMAC is requested"}
	}
	return storeHashCtx(z, &hashCtx{h: hmac.New(a.new, []byte(key))}), nil
}

func nativeHashUpdate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hash_update", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, ctx, err := hashCtxArg(z, args[0], "hash_update")
	if err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[1], "hash_update", "2", "data")
	if err != nil {
		return types.Value{}, err
	}
	_, _ = io.WriteString(ctx.h, data)
	return types.NewBool(true), nil
}

// nativeHashFinal rend le condensat et retire le contexte de la Zone : tout usage ultérieur est refusé.
func nativeHashFinal(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hash_final", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	id, ctx, err := hashCtxArg(z, args[0], "hash_final")
	if err != nil {
		return types.Value{}, err
	}
	binaryOut := optBool(z, args, 1)
	delete(extStateFor(z).hashCtxs, id)
	sum := ctx.h.Sum(nil)
	if binaryOut {
		return newStr(z, string(sum))
	}
	return newStr(z, hex.EncodeToString(sum))
}

func nativeHashCopy(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hash_copy", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, ctx, err := hashCtxArg(z, args[0], "hash_copy")
	if err != nil {
		return types.Value{}, err
	}
	c, ok := ctx.h.(hash.Cloner)
	if !ok {
		return types.Value{}, Error{"hash_copy(): Argument #1 ($context) cannot be copied"}
	}
	dup, err := c.Clone()
	if err != nil {
		return types.Value{}, Error{"hash_copy(): Argument #1 ($context) cannot be copied"}
	}
	return storeHashCtx(z, &hashCtx{h: dup}), nil
}

func nativeHashHMACAlgos(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hash_hmac_algos", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	a, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, algo := range hashAlgos {
		if !algo.hmac {
			continue
		}
		s, err := newStr(z, algo.name)
		if err != nil {
			return types.Value{}, err
		}
		if err = appendVal(z, &a, s); err != nil {
			return types.Value{}, err
		}
	}
	return a, nil
}
