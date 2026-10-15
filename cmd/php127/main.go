// php127 execute le bytecode issu du compilateur PHP.
package main

import (
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/parser"
	"code.hazyhaar.fr/devhoros/php127/sapi"
	"code.hazyhaar.fr/devhoros/php127/vm"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// cliZoneCapacity borne la Zone d'une requête CLI ou HTTP : le chargement
// complet de WordPress dépasse la capacité par défaut du paquet zone.
const cliZoneCapacity uint32 = 64 << 20

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) (exitCode int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stdout, "\nFatal error: internal error: %v\n", r)
			exitCode = 255
		}
	}()
	lint := false
	showVersion := false
	raw := ""
	hasRaw := false
	filename := ""
	serverAddr := ""
	docRoot := ""
	var directives []string
	var scriptArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-n":
		case arg == "-l":
			lint = true
		case arg == "-v", arg == "--version":
			showVersion = true
		case arg == "-r":
			if i+1 == len(args) {
				fmt.Fprintln(stderr, "Error in argument 1, char 2: option requires an argument r")
				return 1
			}
			i++
			raw = args[i]
			hasRaw = true
		case arg == "-S":
			if i+1 == len(args) {
				fmt.Fprintln(stderr, "Error in argument 1, char 2: option requires an argument S")
				return 1
			}
			i++
			serverAddr = args[i]
		case arg == "-t":
			if i+1 == len(args) {
				fmt.Fprintln(stderr, "Error in argument 1, char 2: option requires an argument t")
				return 1
			}
			i++
			docRoot = args[i]
		case arg == "-d":
			if i+1 == len(args) {
				fmt.Fprintln(stderr, "Error in argument 1, char 2: option requires an argument d")
				return 1
			}
			i++
			directives = append(directives, args[i])
		case strings.HasPrefix(arg, "-d"):
			directives = append(directives, strings.TrimPrefix(arg, "-d"))
		case arg == "--":
			// Comme PHP, tout ce qui suit est argument du code en ligne (-r) ou du script lu sur l'entrée standard.
			scriptArgs = args[i+1:]
			i = len(args)
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(stderr, "Unknown option: %s\n", arg)
			return 1
		default:
			if hasRaw {
				scriptArgs = args[i:]
			} else {
				filename = arg
				scriptArgs = args[i+1:]
			}
			i = len(args)
		}
	}
	overrides := parseDirectives(directives)
	capacity := zoneCapacityFor(overrides)
	if showVersion {
		fmt.Fprintln(stdout, "PHP 8.3.6 (cli) (php127 Go 1.27 Zero-GC Engine)")
		return 0
	}
	if serverAddr != "" {
		if docRoot == "" {
			if wd, err := os.Getwd(); err == nil {
				docRoot = wd
			} else {
				docRoot = "."
			}
		}
		srv, err := buildHTTPServer(serverAddr, docRoot, filename, stderr, overrides, capacity)
		if err != nil {
			fmt.Fprintf(stderr, "php127: %v\n", err)
			return 1
		}
		if err := http.ListenAndServe(serverAddr, srv); err != nil {
			fmt.Fprintf(stderr, "php127: %v\n", err)
			return 1
		}
		return 0
	}
	cliArgv := append([]string{filename}, scriptArgs...)
	var src string
	if hasRaw {
		src = "<?php " + raw
		filename = "Command line code"
		cliArgv[0] = "Standard input code"
	} else if filename != "" {
		data, err := os.ReadFile(filename)
		if err != nil {
			fmt.Fprintf(stderr, "Could not open input file: %s\n", filename)
			return 1
		}
		src = string(data)
		if !lint {
			if abs, absErr := filepath.Abs(filename); absErr == nil {
				filename = abs
			}
		}
	} else {
		data, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "php127: %v\n", err)
			return 1
		}
		src = string(data)
		filename = "Standard input code"
		cliArgv[0] = filename
	}
	if lint {
		if err := parser.CheckSyntax(src, filename); err != nil {
			fmt.Fprintf(stdout, "\n%s\nErrors parsing %s\n", err, filename)
			return 255
		}
		fmt.Fprintf(stdout, "No syntax errors detected in %s\n", filename)
		return 0
	}
	prog, err := compiler.CompileSource(src, filename)
	if err != nil {
		fmt.Fprintf(stdout, "\n%s\n", err)
		return 255
	}
	ctx := vm.NewContext()
	ctx.CLIArgv = cliArgv
	ctx.CLIInline = hasRaw || cliArgv[0] == "Standard input code"
	ctx.INIOverrides = overrides
	return vm.Execute(zone.New(capacity), stdout, stderr, prog, ctx) & 255
}

// parseDirectives découpe les directives -d clé=valeur comme PHP : une clé sans signe égal vaut "1", la dernière
// occurrence d'une clé l'emporte ; nil sans directive.
func parseDirectives(directives []string) map[string]string {
	if len(directives) == 0 {
		return nil
	}
	out := make(map[string]string, len(directives))
	for _, d := range directives {
		key, value, ok := strings.Cut(d, "=")
		if !ok {
			value = "1"
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = value
	}
	return out
}

// zoneCapacityFor dimensionne la Zone d'une requête d'après memory_limit (-d memory_limit=128M) : quantité en
// octets avec suffixe K, M ou G comme PHP, bornée à la capacité adressable d'une Zone. Une limite absente,
// illisible ou illimitée (-1) garde cliZoneCapacity, la Zone réservant sa capacité entière à la construction.
func zoneCapacityFor(overrides map[string]string) uint32 {
	limit, ok := parseQuantity(overrides["memory_limit"])
	if !ok || limit <= 0 {
		return cliZoneCapacity
	}
	return uint32(min(limit, int64(math.MaxUint32)) &^ 7)
}

// parseQuantity lit une quantité INI de PHP : entier décimal suivi au plus d'un suffixe K, M ou G, sans
// distinction de casse.
func parseQuantity(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	shift := 0
	switch s[len(s)-1] {
	case 'k', 'K':
		shift = 10
	case 'm', 'M':
		shift = 20
	case 'g', 'G':
		shift = 30
	}
	if shift > 0 {
		s = strings.TrimSpace(s[:len(s)-1])
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	if n > 0 && n > math.MaxInt64>>shift {
		return math.MaxInt64, true
	}
	return n << shift, true
}

func buildHTTPServer(addr, docRoot, routerScript string, stderr io.Writer, overrides map[string]string, capacity uint32) (*sapi.Server, error) {
	absRoot, err := filepath.Abs(docRoot)
	if err != nil {
		return nil, err
	}
	srvName, srvPort := "localhost", "80"
	if h, p, err := net.SplitHostPort(addr); err == nil {
		if h != "" {
			srvName = h
		}
		if p != "" {
			srvPort = p
		}
	}
	runner := sapi.HandlerFunc(func(z *zone.Zone, req *sapi.RequestContext, out io.Writer) error {
		scriptPath := routerScript
		if scriptPath == "" {
			urlPath := "/index.php"
			if req.Request != nil && req.Request.URL != nil && req.Request.URL.Path != "" && req.Request.URL.Path != "/" {
				urlPath = req.Request.URL.Path
			}
			cleanRel := filepath.Clean(strings.TrimPrefix(urlPath, "/"))
			scriptPath = filepath.Join(absRoot, cleanRel)
			if st, err := os.Stat(scriptPath); err == nil && st.IsDir() {
				scriptPath = filepath.Join(scriptPath, "index.php")
			}
		}
		data, err := os.ReadFile(scriptPath)
		if err != nil {
			req.StatusCode = http.StatusNotFound
			_, _ = io.WriteString(out, "Not Found")
			return nil
		}
		prog, err := compiler.CompileSource(string(data), scriptPath)
		if err != nil {
			return err
		}
		ctx := vm.NewContext()
		ctx.INIOverrides = overrides
		vm.Execute(z, out, stderr, prog, ctx)
		return nil
	})
	return sapi.NewServer(runner, sapi.Options{
		ZoneCapacity: capacity,
		ServerConfig: sapi.ServerConfig{
			DocumentRoot: absRoot,
			ServerName:   srvName,
			ServerPort:   srvPort,
		},
	}), nil
}
