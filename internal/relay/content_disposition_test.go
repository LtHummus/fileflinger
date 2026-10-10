package relay

import "testing"

func TestContentDisposition(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{"report.pdf", `attachment; filename="report.pdf"; filename*=UTF-8''report.pdf`},
		{"my file; v2.pdf", `attachment; filename="my file; v2.pdf"; filename*=UTF-8''my%20file%3B%20v2.pdf`},
		{`a"b\c.txt`, `attachment; filename="a_b_c.txt"; filename*=UTF-8''a%22b%5Cc.txt`},
		{"résumé.pdf", `attachment; filename="r_sum_.pdf"; filename*=UTF-8''r%C3%A9sum%C3%A9.pdf`},
		{"it's (1).txt", `attachment; filename="it's (1).txt"; filename*=UTF-8''it%27s%20%281%29.txt`},
	} {
		if got := contentDisposition(tc.name); got != tc.want {
			t.Errorf("contentDisposition(%q)\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}
