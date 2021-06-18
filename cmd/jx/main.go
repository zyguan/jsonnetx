package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-jsonnet"
	"github.com/jmespath/go-jmespath"
	"github.com/spf13/pflag"
	"github.com/zyguan/jsonnetx"
	"gopkg.in/yaml.v2"

	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"
)

var options struct {
	help    bool
	kube    bool
	yaml    bool
	output  string
	jexpr   string
	jpath   []string
	extStr  map[string]string
	extCode map[string]string
	tlaStr  map[string]string
	tlaCode map[string]string
}

var klogFlagSet = flag.NewFlagSet("klog", flag.ExitOnError)

func addGoFlag(name string, full string, short string) {
	arg := pflag.PFlagFromGoFlag(klogFlagSet.Lookup(name))
	arg.Name = full
	arg.Shorthand = short
	usage := []byte(arg.Usage)
	copy(usage, bytes.ToUpper(usage[:1]))
	arg.Usage = string(usage)
	pflag.CommandLine.AddFlag(arg)
}

func initFlags() {
	pflag.BoolVarP(&options.help, "help", "h", false, "This message")
	pflag.BoolVarP(&options.kube, "kube-stream", "k", false, "Write output as a YAML stream of kubernetes resources")
	pflag.BoolVar(&options.yaml, "yaml", false, "Write output in YAML format")
	pflag.StringVarP(&options.output, "output-file", "o", "", "Write to the output file rather than stdout")
	pflag.StringVarP(&options.jexpr, "jexpr", "e", "", "JMESPath expression for extracting resources")
	pflag.StringArrayVarP(&options.jpath, "jpath", "J", []string{}, "Specify an additional library search dir (right-most wins)")
	pflag.StringToStringVarP(&options.extStr, "ext-str", "V", map[string]string{}, "Specify values of external variables as strings")
	pflag.StringToStringVar(&options.extCode, "ext-code", map[string]string{}, "Specify values of external variables as code")
	pflag.StringToStringVarP(&options.tlaStr, "tla-str", "A", map[string]string{}, "Specify values of top-level arguments as strings")
	pflag.StringToStringVar(&options.tlaCode, "tla-code", map[string]string{}, "Specify values of top-level arguments as code")

	pflag.CommandLine.SortFlags = false
	klog.InitFlags(klogFlagSet)
	addGoFlag("v", "verbosity", "v")
}

func setJsonnetParams(vm *jsonnet.VM) {
	for k, v := range options.extStr {
		vm.ExtVar(k, v)
	}
	for k, v := range options.extCode {
		vm.ExtCode(k, v)
	}
	for k, v := range options.tlaStr {
		vm.TLAVar(k, v)
	}
	for k, v := range options.tlaCode {
		vm.TLACode(k, v)
	}
}

func readInput() (string, string) {
	if pflag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: must give filename")
		os.Exit(1)
	} else if pflag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "ERROR: only one filename is allowed")
		os.Exit(1)
	}
	var (
		raw []byte
		err error
	)
	url := pflag.Arg(0)
	if strings.HasPrefix(url, "http") {
		resp, err := http.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: http get %q: %v\n", url, err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			fmt.Fprintf(os.Stderr, "ERROR: http get %q: %v\n", url, resp.Status)
			os.Exit(1)
		}
		raw, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: read from %q: %v\n", url, err)
			os.Exit(1)
		}
	} else {
		raw, err = ioutil.ReadFile(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: read from %q: %v\n", url, err)
			os.Exit(1)
		}
	}

	return url, string(raw)
}

func jsonnetPaths() []string {
	jsonnetPath := filepath.SplitList(os.Getenv("JSONNET_PATH"))
	for _, jpath := range options.jpath {
		jsonnetPath = append(jsonnetPath, jpath)
	}
	for i := 0; i < len(jsonnetPath)/2; i++ {
		jsonnetPath[i], jsonnetPath[len(jsonnetPath)-i-1] = jsonnetPath[len(jsonnetPath)-i-1], jsonnetPath[i]
	}

	return jsonnetPath
}

func main() {
	initFlags()
	pflag.Parse()
	if options.help {
		pflag.Usage()
		os.Exit(0)
	}

	// render
	from, content := readInput()
	vendorPath := os.Getenv("JSONNET_VENDOR")
	if len(vendorPath) == 0 {
		vendorPath = "vendor"
	}
	vm := jsonnet.MakeVM()
	setJsonnetParams(vm)
	vm.Importer(jsonnetx.MakeImporter(from, vendorPath, jsonnetPaths()...))
	result, err := vm.EvaluateSnippet(from, content)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: render input: %v\n", err.Error())
		os.Exit(1)
	}
	var root interface{}
	if err = json.Unmarshal([]byte(result), &root); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: decode result: %v\n", err)
		os.Exit(1)
	}
	// extract by expr
	if len(options.jexpr) > 0 {
		root, err = jmespath.Search(options.jexpr, root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: extract %q from result: %v\n", options.jexpr, err)
			os.Exit(1)
		}
		resultBytes, err := json.Marshal(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: encode result: %v\n", err)
			os.Exit(1)
		}
		result = string(resultBytes)
	}

	// open output
	var out io.Writer
	if len(options.output) == 0 {
		out = os.Stdout
	} else {
		f, err := os.Create(options.output)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: open output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}

	// write output
	if !options.kube {
		if options.yaml {
			yaml.NewEncoder(out).Encode(root)
		} else {
			fmt.Fprintln(out, result)
		}
		return
	}
	enc := yaml.NewEncoder(out)
	for _, manifest := range jsonnetx.ExtractManifestTo(nil, root) {
		err := enc.Encode(manifest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: encode item: %v\n", err)
			os.Exit(1)
		}
	}
}

func init() {
	jsonnetx.L = klogr.New().WithName("jx")
}
