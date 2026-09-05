package site

import "testing"

func TestModuleVersionFromPath(t *testing.T) {
	tests := []struct {
		path        string
		wantModule  string
		wantVersion string
		wantOK      bool
	}{
		{path: "/github.com/worldline-go/wkafka@v0.6.7", wantModule: "github.com/worldline-go/wkafka", wantVersion: "v0.6.7", wantOK: true},
		{path: "/example.com/mod@v1.2.3/sub/package", wantModule: "example.com/mod", wantVersion: "v1.2.3", wantOK: true},
		{path: "/example.com/mod@latest"},
		{path: "/example.com/mod"},
		{path: "/search?q=example.com/mod@v1.2.3"},
		{path: "//example.com/mod@v1.2.3"},
		{path: "https://example.com/mod@v1.2.3"},
		{path: "/example.com/mod@v1.2.3/../other"},
		{path: "/example.com/mod@v1.2.3//other"},
		{path: "/example.com/mod@v1.2.3/"},
		{path: "/example.com/mod@v1.2.3/sub@v1.0.0"},
		{path: "/example.com/mod@v1.2.3?next=//evil.test"},
		{path: "/example.com/mod@v1.2.3#fragment"},
		{path: "/example.com/mod@v1.2"},
		{path: "/example.com/mod@v1.2.3+metadata"},
		{path: "/example.com/mod@v2.0.0"},
		{path: "/example.com/mod/v2@v2.0.0", wantModule: "example.com/mod/v2", wantVersion: "v2.0.0", wantOK: true},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			modulePath, version, ok := moduleVersionFromPath(test.path)
			if modulePath != test.wantModule || version != test.wantVersion || ok != test.wantOK {
				t.Fatalf("moduleVersionFromPath(%q) = (%q, %q, %t), want (%q, %q, %t)", test.path, modulePath, version, ok, test.wantModule, test.wantVersion, test.wantOK)
			}
		})
	}
}
