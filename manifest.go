package jsonnetx

import "sort"

func ExtractManifestTo(dst []interface{}, obj interface{}) []interface{} {
	if IsManifest(obj) {
		dst = append(dst, obj)
		return dst
	}
	switch x := obj.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			dst = ExtractManifestTo(dst, x[k])
		}
	case []interface{}:
		for _, item := range x {
			dst = ExtractManifestTo(dst, item)
		}
	}
	return dst
}

func IsManifest(x interface{}) bool {
	obj, ok := x.(map[string]interface{})
	if !ok || obj == nil {
		return false
	}
	if v, ok := obj["apiVersion"].(string); !ok || len(v) == 0 {
		return false
	}
	if v, ok := obj["kind"].(string); !ok || len(v) == 0 {
		return false
	}
	return true
}
