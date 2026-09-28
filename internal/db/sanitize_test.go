package db

import "testing"

func TestSanitize(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  string
		count int
	}{
		{"plain", "SQLite trigram 支援 CJK", "SQLite trigram 支援 CJK", 0},
		{"private_block", "前 <private>密碼是 abc</private> 後", "前  後", 1},
		{"private_multiline_case", "a <PRIVATE>x\ny\n</Private> b <private>z</private>", "a  b", 2},
		{"pem", "key:\n-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----\nend", "key:\n[REDACTED]\nend", 1},
		{"aws", "id AKIAABCDEFGHIJKLMNOP here", "id [REDACTED] here", 1},
		{"anthropic", "k sk-ant-api03-abcdefghijklmnopqrstuv", "k [REDACTED]", 1},
		{"openai", "k sk-abcdefghijklmnopqrstuvwx", "k [REDACTED]", 1},
		{"github", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", "[REDACTED]", 1},
		{"gitlab", "glpat-abcdefghij1234567890", "[REDACTED]", 1},
		{"jwt", "Bearer eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0.SflKxwRJSMeKKF2QT4f", "Bearer [REDACTED]", 1},
		{"password_eq", "password=abcdef123 ok", "password=[REDACTED] ok", 1},
		{"api_key_colon_quoted", `api_key: "zzzzzz99"`, `api_key: "[REDACTED]"`, 1},
		{"token_with_anthropic_key", "token=sk-ant-api03-abcdefghijklmnopqrstuv", "token=[REDACTED]", 1},
		{"chinese_prose_untouched", "後台操作員改密碼、輪換 token、providerpassword 不動", "後台操作員改密碼、輪換 token、providerpassword 不動", 0},
		{"short_cjk_value", "secret: 這是秘密", "secret: 這是秘密", 0},
		{"short_value", "pwd=abc", "pwd=abc", 0},
		{"path_value_pwd_colon", "pwd: /Users/white/github/foo", "pwd: /Users/white/github/foo", 0},
		{"path_value_pwd_eq", "PWD=/opt/app/current", "PWD=/opt/app/current", 0},
		{"keyword_word_boundary", "max_token=409600", "max_token=409600", 0},
		{"token_with_anthropic_key_trailing_dot", "token=sk-ant-api03-abcdefghijklmnopqrstuv.", "token=[REDACTED].", 1},
		{"trim", "  <private>x</private> keep  ", "keep", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, n := Sanitize(c.in)
			if got != c.want {
				t.Errorf("content = %q, want %q", got, c.want)
			}
			if n != c.count {
				t.Errorf("count = %d, want %d", n, c.count)
			}
		})
	}
}
