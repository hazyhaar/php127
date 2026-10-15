// php127 execute le bytecode issu du compilateur PHP.
package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/parser"
	"code.hazyhaar.fr/devhoros/php127/sapi"
	"code.hazyhaar.fr/devhoros/php127/vm"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

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
			if i+1 < len(args) {
				filename = args[i+1]
			}
			i = len(args)
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(stderr, "Unknown option: %s\n", arg)
			return 1
		default:
			filename = arg
			i = len(args)
		}
	}
	_ = directives
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
		srv, err := buildHTTPServer(serverAddr, docRoot, filename, stderr)
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
	var src string
	if hasRaw {
		src = "<?php " + raw
		filename = "Command line code"
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
	return vm.Execute(zone.New(zone.DefaultCapacity), stdout, stderr, prog, vm.NewContext()) & 255
}

func buildHTTPServer(addr, docRoot, routerScript string, stderr io.Writer) (*sapi.Server, error) {
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
		vm.Execute(z, out, stderr, prog, vm.NewContext())
		return nil
	})
	return sapi.NewServer(runner, sapi.Options{
		ServerConfig: sapi.ServerConfig{
			DocumentRoot: absRoot,
			ServerName:   srvName,
			ServerPort:   srvPort,
		},
	}), nil
}
