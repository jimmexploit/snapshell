package blog

import "testing"

func TestDetectLang(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"empty", "", "text"},
		{"whitespace only", " \n\t\n", "text"},

		// Shell sessions.
		{"bash prompt", "$ ls -la\n-rw-r--r-- 1 jimmex jimmex 0 Aug 16 10:00 file\n", "bash"},
		{"root prompt", "# whoami\nroot\n", "bash"},
		{"zsh prompt", "% echo hi\nhi\n", "bash"},
		{"powerline prompt", "┌─[192.168.37.140]─[jimmex@attacker]─[~/snapshell]\n└──╼ [★]$ ls -la\n", "bash"},
		{"fish prompt", "❯ ls\ndocs\n", "bash"},
		{"nmap scan session", "$ nmap -sV 10.10.10.1\nPORT   STATE SERVICE\n80/tcp open  http\n", "bash"},

		// Shebangs.
		{"shebang python", "#!/usr/bin/env python3\nprint('hi')\n", "python"},
		{"shebang bash", "#!/bin/bash\necho hi\n", "bash"},
		{"shebang node", "#!/usr/bin/env node\nconsole.log(1)\n", "javascript"},

		// Source code by content.
		{"go source", "package main\n\nfunc main() {}\n", "go"},
		{"go var only", "package foo\nvar x = 1\n", "go"},
		{"python def", "def greet(name):\n    return f'hi {name}'\n", "python"},
		{"python import", "import os\nprint(os.getcwd())\n", "python"},
		{"python main guard", "if __name__ == '__main__':\n    main()\n", "python"},
		{"python for loop", "for i in range(5):\n    print(i)\n", "python"},
		{"python for loop one line", "for i in range(5): print(i)\n", "python"},
		{"python while", "while True:\n    break\n", "python"},
		{"python if colon", "if x > 5:\n    print(x)\n", "python"},
		{"python with", "with open('file') as f:\n    data = f.read()\n", "python"},
		{"bash for loop stays bash", "for i in 1 2 3; do\n  echo $i\ndone\n", "bash"},
		{"yaml doc", "---\nname: box\nports:\n  - 22\n", "yaml"},
		{"yaml kv", "host: 10.10.10.1\nuser: root\n", "yaml"},
		{"json", "{\"name\": \"box\", \"ports\": [22, 80]}", "json"},
		{"json array", "[1, 2, 3]", "json"},
		{"html", "<!DOCTYPE html>\n<html><body>hi</body></html>\n", "html"},
		{"toml", "[server]\nhost = \"0.0.0.0\"\nport = 8080\n", "toml"},

		// Fallbacks: anything unrecognized is a shell command at heart, so
		// it lands in a bash fence — which is what makes every markdown
		// viewer syntax-color the block.
		{"plain command no prompt", "nmap -sV 10.10.10.1\n", "bash"},
		{"prose", "Remember to enumerate the box slowly.\nCheck every service.\n", "bash"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectLang(tc.text); got != tc.want {
				t.Errorf("DetectLang(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestDetectLangCommentNotMisreadAsRootPrompt(t *testing.T) {
	text := "# config lives here\ndef load():\n    pass\n"
	if got := DetectLang(text); got != "python" {
		t.Errorf("comment line misread as prompt, got %q want python", got)
	}
}

func TestDetectLangTerminalSessionWinsOverContent(t *testing.T) {
	// A shell session that happens to display a Go file is still a shell
	// session: the prompt must take precedence.
	text := "$ cat main.go\npackage main\n\nfunc main() {}\n"
	if got := DetectLang(text); got != "bash" {
		t.Errorf("terminal session misdetected as %q, want bash", got)
	}
}
