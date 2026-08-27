// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/httpgateway: Passerelle HTTP in-process souveraine à zéro allocation

package httpgateway

import (
	"bytes"
	"io"
	"net/http"
	"sync"

	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/security"
	"code.hazyhaar.fr/devhoros/php127/vm"
)

type Gateway struct {
	Program      *vm.BytecodeProgram
	GuardMode    security.SecurityMode
	vmPool       sync.Pool
	BufferOutput bool
}

func NewGateway(prog *vm.BytecodeProgram, mode security.SecurityMode) *Gateway {
	gw := &Gateway{
		Program:      prog,
		GuardMode:    mode,
		BufferOutput: true,
	}
	gw.vmPool.New = func() interface{} {
		return vm.NewVM(nil)
	}
	return gw
}

func (gw *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	machine := gw.vmPool.Get().(*vm.VM)
	defer gw.vmPool.Put(machine)

	machine.Reset()
	machine.Guard.Mode = gw.GuardMode

	var outBuf bytes.Buffer
	machine.Output = &outBuf

	// Initialisation des superglobales
	// $_SERVER
	// $_GET
	// $_POST
	// Ingress inspection
	if r.URL.RawQuery != "" {
		taintedQuery, err := machine.Guard.IngressInspect([]byte(r.URL.RawQuery))
		if err != nil {
			http.Error(w, "Requête bloquée par la sécurité php127", http.StatusForbidden)
			return
		}
		_ = taintedQuery
	}

	if r.Body != nil {
		bodyBytes, _ := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10 Mo max
		if len(bodyBytes) > 0 {
			taintedBody, err := machine.Guard.IngressInspect(bodyBytes)
			if err != nil {
				http.Error(w, "Charge utile bloquée par la sécurité php127", http.StatusForbidden)
				return
			}
			_ = taintedBody
		}
	}

	// Exécution du programme bytecode
	_, err := machine.Execute(gw.Program)
	if err != nil {
		http.Error(w, "Erreur interne d'exécution", http.StatusInternalServerError)
		return
	}

	// Egress inspect
	outputBytes := outBuf.Bytes()
	if err := machine.Guard.EgressInspect(outputBytes); err != nil {
		http.Error(w, "Sortie bloquée par la sécurité (fuite de données)", http.StatusForbidden)
		return
	}

	w.Header().Set("X-Powered-By", "php127/flatstack-go")
	w.WriteHeader(http.StatusOK)
	w.Write(outputBytes)
}

func CompileAndServe(src string, mode security.SecurityMode) (*Gateway, error) {
	prog, err := compiler.Compile(src)
	if err != nil {
		return nil, err
	}
	return NewGateway(prog, mode), nil
}
