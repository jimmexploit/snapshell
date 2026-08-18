package daemon

import "testing"

func TestCountEntries(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"header only", "# acme\n", 0},
		{"one image with caption", "# acme\n\n![](attachments/001.png)\ninitial foothold\n", 1},
		{"two images", "# acme\n\n![](attachments/001.png)\n\n![](attachments/002.png)\n", 2},
		{"code with blank lines inside", "# acme\n\n```bash\n$ ls\none\n\ntwo\n```\n", 1},
		{"code then note", "# acme\n\n```bash\nwhoami\n```\n\nFound creds.\n", 2},
		{"widened fence not confused", "# acme\n\n````text\n```\nstill inside\n````\n", 1},
	}
	for _, tc := range cases {
		if got := countEntries(tc.in); got != tc.want {
			t.Errorf("%s: countEntries = %d, want %d", tc.name, got, tc.want)
		}
	}
}
