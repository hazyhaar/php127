// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/cmd/php127: Binaire CLI canonique et serveur HTTP embarqué

package main

import (
	"bufio"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/httpgateway"
	"code.hazyhaar.fr/devhoros/php127/security"
	"code.hazyhaar.fr/devhoros/php127/vm"
)

const Version = "php127 v1.0.0 (Flatstack127 Go runtime PHP 8.4-compatible, Sovereign Zero-Alloc Edition)"

func main() {
	evalCode := flag.String("r", "", "Évalue directement le code PHP en paramètre")
	replMode := flag.Bool("a", false, "Lance le mode interactif REPL")
	serverAddr := flag.String("S", "", "Lance un serveur HTTP embarqué sur l'adresse spécifiée (ex: :8080)")
	auditMode := flag.Bool("audit", false, "Active le mode de sécurité Audit (non-bloquant)")
	showVer := flag.Bool("v", false, "Affiche les informations de version")
	flag.BoolVar(showVer, "version", false, "Affiche les informations de version")

	flag.Parse()

	if *showVer {
		fmt.Println(Version)
		return
	}

	secMode := security.ModeEnforce
	if *auditMode {
		secMode = security.ModeAudit
	}

	// 1. Mode Évaluation en ligne -r
	if *evalCode != "" {
		executeSource(*evalCode, secMode)
		return
	}

	// 2. Mode Serveur HTTP -S
	if *serverAddr != "" {
		var scriptContent string = "<?php echo 'php127 server active'; ?>"
		args := flag.Args()
		if len(args) > 0 {
			b, err := os.ReadFile(args[0])
			if err == nil {
				scriptContent = string(b)
			}
		}
		gw, err := httpgateway.CompileAndServe(scriptContent, secMode)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Erreur compilation script serveur: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Démarrage du serveur php127 sur http://%s\n", *serverAddr)
		if err := http.ListenAndServe(*serverAddr, gw); err != nil {
			fmt.Fprintf(os.Stderr, "Erreur serveur: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 3. Mode REPL Interactif -a
	if *replMode {
		runREPL(secMode)
		return
	}

	// 4. Mode Fichier script
	args := flag.Args()
	if len(args) > 0 {
		filePath := args[0]
		b, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Impossible d'ouvrir le fichier %s: %v\n", filePath, err)
			os.Exit(1)
		}
		executeSource(string(b), secMode)
		return
	}

	// Aide par défaut
	fmt.Println(Version)
	fmt.Println("\nUsage:")
	fmt.Println("  php127 script.php           Exécute un fichier de script PHP")
	fmt.Println("  php127 -r \"code\"            Évalue une ligne de code PHP")
	fmt.Println("  php127 -a                   Lance la console interactive REPL")
	fmt.Println("  php127 -S 127.0.0.1:8080    Lance le serveur HTTP in-process")
	fmt.Println("  php127 --audit script.php   Exécute avec journalisation de sécurité non-bloquante")
}

func executeSource(src string, mode security.SecurityMode) {
	cleanSrc := strings.TrimSpace(src)
	if strings.HasPrefix(cleanSrc, "<?php") {
		cleanSrc = strings.TrimPrefix(cleanSrc, "<?php")
	}
	if strings.HasSuffix(cleanSrc, "?>") {
		cleanSrc = strings.TrimSuffix(cleanSrc, "?>")
	}

	prog, err := compiler.Compile(cleanSrc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erreur de compilation: %v\n", err)
		os.Exit(1)
	}

	machine := vm.NewVM(os.Stdout)
	machine.Guard.Mode = mode
	_, err = machine.Execute(prog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erreur d'exécution: %v\n", err)
		os.Exit(1)
	}
}

func runREPL(mode security.SecurityMode) {
	fmt.Println(Version)
	fmt.Println("Mode interactif actif. Tapez 'exit' ou 'quit' pour quitter.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	machine := vm.NewVM(os.Stdout)
	machine.Guard.Mode = mode

	for {
		fmt.Print("php127 > ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "exit" || line == "quit" {
			break
		}
		if line == "" {
			continue
		}

		prog, err := compiler.Compile(line)
		if err != nil {
			fmt.Printf("Erreur: %v\n", err)
			continue
		}

		res, err := machine.Execute(prog)
		if err != nil {
			fmt.Printf("Exception: %v\n", err)
			continue
		}
		if res.Type != 0 && res.Type != 1 { // Non-undef et non-null
			fmt.Printf("=> %s\n", res.String())
		}
	}
}
