package tui

import "testing"

func TestStripHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text unchanged",
			input: "Hello, world!",
			want:  "Hello, world!",
		},
		{
			name:  "simple tags stripped",
			input: "<b>Bold</b> and <i>italic</i>",
			want:  "Bold and italic",
		},
		{
			name:  "br tags become newlines",
			input: "Line 1<br>Line 2<br/>Line 3",
			want:  "Line 1\nLine 2\nLine 3",
		},
		{
			name:  "paragraph tags",
			input: "<p>First paragraph</p><p>Second paragraph</p>",
			want:  "First paragraph\nSecond paragraph",
		},
		{
			name:  "list items become bullets",
			input: "<ul><li>Item 1</li><li>Item 2</li><li>Item 3</li></ul>",
			want:  "• Item 1\n\n• Item 2\n\n• Item 3",
		},
		{
			name:  "HTML entities decoded",
			input: "A &amp; B &lt; C &gt; D &quot;quoted&quot;",
			want:  `A & B < C > D "quoted"`,
		},
		{
			name:  "nbsp entity",
			input: "Hello&nbsp;World",
			want:  "Hello World",
		},
		{
			name:  "excessive newlines collapsed",
			input: "A<br><br><br><br><br>B",
			want:  "A\n\nB",
		},
		{
			name:  "complex Sparkle-style HTML",
			input: `<h2>Version 2.0</h2><ul><li>New feature A</li><li>Bug fix B</li></ul><p>Thanks for updating!</p>`,
			want:  "Version 2.0\n• New feature A\n\n• Bug fix B\nThanks for updating!",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only tags",
			input: "<div><span></span></div>",
			want:  "",
		},
		{
			name:  "attributes in tags",
			input: `<a href="https://example.com">Click here</a>`,
			want:  "Click here",
		},
		{
			name:  "div closing tags",
			input: "<div>Block 1</div><div>Block 2</div>",
			want:  "Block 1\nBlock 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTML(tt.input)
			if got != tt.want {
				t.Errorf("stripHTML() =\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}
