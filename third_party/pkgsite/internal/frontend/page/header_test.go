package page

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/safehtml/template"
)

func TestHeaderStdlibLink(t *testing.T) {
	tmpl, err := template.ParseFiles("../../../static/shared/header/header.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		local bool
		want  int
	}{
		{"local", true, 1},
		{"public", false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := tmpl.ExecuteTemplate(&out, "header", BasePage{LocalMode: test.local}); err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(out.String(), `<a class="go-Header-stdlib" href="/std">Stdlib</a>`); got != test.want {
				t.Errorf("stdlib link count = %d, want %d", got, test.want)
			}
		})
	}
}
