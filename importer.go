package jsonnetx

import (
	"encoding/json"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/google/go-jsonnet"
	jb "github.com/jsonnet-bundler/jsonnet-bundler/pkg"
	"github.com/jsonnet-bundler/jsonnet-bundler/spec/v1"
	"github.com/jsonnet-bundler/jsonnet-bundler/spec/v1/deps"
	"github.com/pkg/errors"
)

func MakeImporter(rootFrom string, vendorPath string, jsonnetPath ...string) jsonnet.Importer {
	t := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	jpath := make([]string, 0, len(jsonnetPath)+1)
	jpath = append(jpath, vendorPath)
	jpath = append(jpath, jsonnetPath...)

	return &importer{
		rootFrom:   rootFrom,
		vendorPath: vendorPath,
		http:       &http.Client{Transport: t},
		local:      &jsonnet.FileImporter{JPaths: jpath},
		cache:      map[string]jsonnet.Contents{},
	}
}

type importer struct {
	rootFrom   string
	vendorPath string
	bundled    bool

	http  *http.Client
	local *jsonnet.FileImporter
	cache map[string]jsonnet.Contents
}

func (i *importer) Import(from string, path string) (jsonnet.Contents, string, error) {
	c, foundAt, err := i.local.Import(from, path)
	if err == nil {
		return c, foundAt, nil
	}
	if len(from) == 0 || strings.HasPrefix(from, "http") {
		c, foundAt, err = i.httpImport(from, path)
		if err == nil {
			return c, foundAt, nil
		}
	}
	if err = i.ensureVendor(); err != nil {
		return jsonnet.Contents{}, "", err
	}
	return i.local.Import(from, path)
}

func (i *importer) httpImport(from string, path string) (jsonnet.Contents, string, error) {
	if len(from) == 0 {
		from = i.rootFrom
	}
	fromURL, err := url.Parse(from)
	if err != nil {
		return jsonnet.Contents{}, "", errors.Wrap(err, "invalid import path")
	}
	if fromURL.Scheme != "http" && fromURL.Scheme != "https" {
		return jsonnet.Contents{}, "", errors.New("invalid schema: " + fromURL.Scheme)
	}
	fromURL.Path = filepath.Join(filepath.Dir(fromURL.Path), path)

	foundAt := fromURL.String()
	if c, ok := i.cache[foundAt]; ok {
		return c, foundAt, nil
	}
	resp, err := i.http.Get(foundAt)
	if err != nil {
		L.V(1).Info("http import", "from", from, "path", path, "err", err)
		return jsonnet.Contents{}, "", errors.WithStack(err)
	}
	L.V(2).Info("http import", "from", from, "path", path, "status", resp.StatusCode)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return jsonnet.Contents{}, "", errors.New("got " + resp.Status + " when import from " + foundAt)
	}
	bs, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return jsonnet.Contents{}, "", errors.WithStack(err)
	}

	c := jsonnet.MakeContents(string(bs))
	i.cache[foundAt] = c
	return c, foundAt, nil
}

func (i *importer) ensureVendor() error {
	if i.bundled {
		return nil
	}
	defer func() { i.bundled = true }()

	jf, ls, err := i.resolveJsonnetFile()
	if err != nil {
		L.V(1).Info("skip vendor", "err", err)
		return nil
	}
	err = os.MkdirAll(filepath.Join(i.vendorPath, ".tmp"), 0755)
	if err != nil {
		return errors.Wrap(err, "create vendor directory")
	}
	_, err = jb.Ensure(jf, i.vendorPath, ls)
	if err != nil {
		return errors.Wrap(err, "ensure vendor directory")
	}

	L.V(1).Info("vendor created", "jpath", strings.Join(i.local.JPaths, ":"))
	i.local = &jsonnet.FileImporter{JPaths: i.local.JPaths}
	return nil
}

func (i *importer) resolveJsonnetFile() (spec.JsonnetFile, map[string]deps.Dependency, error) {
	base, err := url.Parse(i.rootFrom)
	if err != nil {
		return spec.JsonnetFile{}, nil, errors.Wrap(err, "invalid root url")
	}
	readFile := func(name string) (*spec.JsonnetFile, error) {
		var jf spec.JsonnetFile
		target := *base
		target.Path = filepath.Join(target.Path, name)
		targetURL := target.String()
		resp, err := i.http.Get(targetURL)
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
	for base.Path = filepath.Dir(base.Path); base.Path != "." && base.Path != "/"; base.Path = filepath.Dir(base.Path) {
		jf, err := readFile("jsonnetfile.json")
		if err != nil {
			continue
		}
		lf, err := readFile("jsonnetfile.lock.json")
		if err != nil {
			return *jf, map[string]deps.Dependency{}, nil
		} else {
			return *jf, lf.Dependencies, nil
		}
	}

	jf, err := readFile("jsonnetfile.json")
	if err != nil {
		return spec.JsonnetFile{}, nil, errors.New("cannot found jsonnetfile.json")
	}
	lf, err := readFile("jsonnetfile.lock.json")
	if err != nil {
		return *jf, map[string]deps.Dependency{}, nil
	} else {
		return *jf, lf.Dependencies, nil
	}
}

func init() {
	color.Output = ioutil.Discard
	color.Error = ioutil.Discard
}
