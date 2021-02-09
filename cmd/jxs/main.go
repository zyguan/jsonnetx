package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/go-jsonnet"
	"github.com/jsonnet-bundler/jsonnet-bundler/spec/v1"
	"github.com/jsonnet-bundler/jsonnet-bundler/spec/v1/deps"
	"github.com/pkg/errors"
	"github.com/zyguan/jsonnetx"
	"gopkg.in/yaml.v2"

	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"
)

var L = klogr.New().WithName("jxs")

type renderRequest struct {
	Main    string                     `json:"main"`
	Lock    string                     `json:"lock"`
	Format  string                     `json:"format"`
	ExtVar  map[string]string          `json:"extVar"`
	ExtCode map[string]json.RawMessage `json:"extCode"`
	TLAVar  map[string]string          `json:"tlaVar"`
	TLACode map[string]json.RawMessage `json:"tlaCode"`
}

func (rr *renderRequest) readMainContent() ([]byte, error) {
	resp, err := http.Get(rr.Main)
	if err != nil {
		return nil, errors.Wrap(err, "get main template")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("got " + resp.Status)
	}
	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "read main content")
	}
	return content, nil
}

func (rr *renderRequest) initVM(vm *jsonnet.VM) {
	for k, v := range rr.ExtVar {
		vm.ExtVar(k, v)
	}
	for k, v := range rr.ExtCode {
		vm.ExtCode(k, string(v))
	}
	for k, v := range rr.TLAVar {
		vm.TLAVar(k, v)
	}
	for k, v := range rr.TLACode {
		vm.TLACode(k, string(v))
	}
}

func (rr *renderRequest) resolveDependencies() (map[string]deps.Dependency, error) {
	if len(rr.Lock) > 0 {
		target, err := url.Parse(rr.Lock)
		if err != nil {
			return nil, errors.Wrap(err, "invalid lock url")
		}
		jf, err := readJsonnetFile(target, "")
		if err != nil {
			return nil, errors.Wrap(err, "read from lock url")
		}
		return jf.Dependencies, nil
	}
	base, err := url.Parse(rr.Main)
	if err != nil {
		return nil, errors.Wrap(err, "invalid main url")
	}
	for base.Path = filepath.Dir(base.Path); base.Path != "." && base.Path != "/"; base.Path = filepath.Dir(base.Path) {
		lf, err := readJsonnetFile(base, "jsonnetfile.lock.json")
		if err == nil {
			return lf.Dependencies, nil
		}
	}
	lf, err := readJsonnetFile(base, "jsonnetfile.lock.json")
	if err == nil {
		return lf.Dependencies, nil
	}
	return nil, errors.New("cannot resolve dependencies")
}

func readJsonnetFile(base *url.URL, name string) (*spec.JsonnetFile, error) {
	var jf spec.JsonnetFile
	target := *base
	if len(name) > 0 {
		target.Path = filepath.Join(target.Path, name)
	}
	targetURL := target.String()
	resp, err := http.Get(targetURL)
	if err != nil {
		L.V(1).Info("http load jsonnetfile", "url", targetURL, "err", err)
		return nil, err
	}
	L.V(2).Info("http load jsonnetfile", "url", targetURL, "status", resp.StatusCode)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("got " + resp.Status)
	}
	if err = json.NewDecoder(resp.Body).Decode(&jf); err != nil {
		return nil, err
	}
	return &jf, nil
}

func dependencyDigest(dependencies map[string]deps.Dependency) string {
	keys := make([]string, 0, len(dependencies))
	for k := range dependencies {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha1.New()
	for _, k := range keys {
		dep := dependencies[k]
		h.Write([]byte(k))
		h.Write([]byte(dep.Version))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type render struct {
	vendorHome string
}

func (h *render) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	raw, err := ioutil.ReadAll(r.Body)
	if err != nil {
		L.Error(err, "read request body")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req renderRequest
	if err = json.Unmarshal(raw, &req); err != nil {
		L.Error(err, "decode request body", "body", string(raw))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	content, err := req.readMainContent()
	if err != nil {
		L.Error(err, "read main content")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dependencies, err := req.resolveDependencies()
	if err != nil {
		L.Error(err, "resolve dependencies")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	vendorPath := path.Join(h.vendorHome, dependencyDigest(dependencies))

	vm := jsonnet.MakeVM()
	vm.Importer(jsonnetx.MakeImporter(req.Main, vendorPath))
	req.initVM(vm)
	result, err := vm.EvaluateAnonymousSnippet(req.Main, string(content))
	if err != nil {
		L.Error(err, "eval main content")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if req.Format == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write([]byte(result))
		return
	}

	var root interface{}
	if err = json.Unmarshal([]byte(result), &root); err != nil {
		L.Error(err, "decode result")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	enc := yaml.NewEncoder(w)
	for _, manifest := range jsonnetx.ExtractManifestTo(nil, root) {
		if err := enc.Encode(manifest); err != nil {
			L.Error(err, "encode item")
			return
		}
	}
}

type response struct {
	http.ResponseWriter
	status int
}

func (r *response) WriteHeader(status int) {
	r.ResponseWriter.WriteHeader(status)
	r.status = status
}

func withAccessLog(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()
		resp := &response{w, http.StatusOK}
		h.ServeHTTP(resp, r)
		L.Info("access log",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
			"status", resp.status,
			"duration", time.Now().Sub(startTime).String(),
		)
	})
}

func main() {
	var opts struct {
		render
		addr string
	}
	klog.InitFlags(nil)
	flag.StringVar(&opts.addr, "l", ":8080", "address to listen on")
	flag.StringVar(&opts.vendorHome, "vendor", "vendor.d", "home directory for jsonnet vendors")
	flag.Parse()

	http.Handle("/render", withAccessLog(&opts.render))
	http.Handle("/", withAccessLog(http.NotFoundHandler()))

	L.Info("listening on " + opts.addr)
	http.ListenAndServe(":8080", nil)
}

func init() {
	jsonnetx.L = L
}
