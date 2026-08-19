package skills

import "testing"

func TestParseAddArguments(t *testing.T) {
	tests := []struct {
		name       string
		arguments  []string
		wantName   string
		wantSource string
	}{
		{
			name:       "GitHub shorthand",
			arguments:  []string{"unslop@poteto/noodle"},
			wantName:   "unslop",
			wantSource: "https://github.com/poteto/noodle",
		},
		{
			name:       "explicit source",
			arguments:  []string{"unslop", "--from", "https://git.example.com/poteto/noodle.git"},
			wantName:   "unslop",
			wantSource: "https://git.example.com/poteto/noodle.git",
		},
		{
			name:       "GitHub source shorthand",
			arguments:  []string{"unslop", "--from", "poteto/noodle"},
			wantName:   "unslop",
			wantSource: "https://github.com/poteto/noodle",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name, source, err := ParseAddArguments(test.arguments)
			if err != nil {
				t.Fatalf("ParseAddArguments() error = %v", err)
			}
			if name != test.wantName || source != test.wantSource {
				t.Errorf("ParseAddArguments() = %q, %q; want %q, %q", name, source, test.wantName, test.wantSource)
			}
		})
	}
}

func TestParseAddArgumentsRejectsAmbiguousOrUnsafeInput(t *testing.T) {
	for _, arguments := range [][]string{
		{"unslop"},
		{"../unslop@poteto/noodle"},
		{"unslop@poteto/noodle/extra"},
		{"Unslop@poteto/noodle"},
	} {
		if _, _, err := ParseAddArguments(arguments); err == nil {
			t.Errorf("ParseAddArguments(%q) error = nil", arguments)
		}
	}
}
