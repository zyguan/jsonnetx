package jsonnetx

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsManifest(t *testing.T) {
	for i, tt := range []struct {
		obj interface{}
		ok  bool
	}{
		{nil, false},
		{map[string]interface{}(nil), false},
		{map[string]interface{}{}, false},
		{map[string]interface{}{"apiVersion": "", "kind": ""}, false},
		{map[string]interface{}{"apiVersion": "foo", "kind": nil}, false},
		{map[string]interface{}{"apiVersion": nil, "kind": "bar"}, false},
		{map[string]interface{}{"apiVersion": "foo", "kind": "bar"}, true},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			require.Equal(t, tt.ok, IsManifest(tt.obj))
		})
	}
}
