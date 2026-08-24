package main

import (
	"testing"
	"time"
)

func TestParseMMSS(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{name: "minutes and seconds", input: "10:30", want: 10*time.Minute + 30*time.Second},
		{name: "leading and trailing spaces", input: " 01:05 ", want: time.Minute + 5*time.Second},
		{name: "zero seconds", input: "2:00", want: 2 * time.Minute},
		{name: "maximum seconds", input: "0:59", want: 59 * time.Second},
		{name: "zero duration", input: "0:00", wantErr: true},
		{name: "negative minutes", input: "-1:00", wantErr: true},
		{name: "negative seconds", input: "1:-1", wantErr: true},
		{name: "seconds too large", input: "1:60", wantErr: true},
		{name: "missing separator", input: "90", wantErr: true},
		{name: "non numeric minutes", input: "x:10", wantErr: true},
		{name: "non numeric seconds", input: "1:x", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseMMSS(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseMMSS(%q) error = nil, want error", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMMSS(%q) returned unexpected error: %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("parseMMSS(%q) = %v, want %v", test.input, got, test.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		input time.Duration
		want  string
	}{
		{input: 5 * time.Second, want: "00:05"},
		{input: time.Minute + 2*time.Second, want: "01:02"},
		{input: 10*time.Minute + 30*time.Second, want: "10:30"},
		{input: 90 * time.Minute, want: "90:00"},
		{input: 1500 * time.Millisecond, want: "00:02"},
	}

	for _, test := range tests {
		if got := formatDuration(test.input); got != test.want {
			t.Errorf("formatDuration(%v) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		limit int
		want  string
	}{
		{name: "short string", input: "Firefox", limit: 10, want: "Firefox"},
		{name: "exact limit", input: "abcdef", limit: 6, want: "abcdef"},
		{name: "ascii truncation", input: "abcdefgh", limit: 5, want: "abcde…"},
		{name: "unicode truncation", input: "วิดีโอทดสอบ", limit: 5, want: "วิดีโ…"},
		{name: "zero limit", input: "abc", limit: 0, want: "…"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := truncateRunes(test.input, test.limit); got != test.want {
				t.Errorf("truncateRunes(%q, %d) = %q, want %q", test.input, test.limit, got, test.want)
			}
		})
	}
}

func TestParseAudioEventLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  AudioEvent
		valid bool
	}{
		{
			name:  "change sink input",
			input: "Event 'change' on sink-input #45",
			want:  AudioEvent{Kind: "change", Category: "sink-input", Index: 45},
			valid: true,
		},
		{
			name:  "leading whitespace",
			input: "  Event 'new' on sink #7  ",
			want:  AudioEvent{Kind: "new", Category: "sink", Index: 7},
			valid: true,
		},
		{
			name:  "remove event",
			input: "Event 'remove' on source-output #123",
			want:  AudioEvent{Kind: "remove", Category: "source-output", Index: 123},
			valid: true,
		},
		{name: "malformed line", input: "not an audio event", valid: false},
		{name: "missing index", input: "Event 'change' on sink-input", valid: false},
		{name: "missing event kind", input: "Event on sink-input #45", valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, valid := parseAudioEventLine(test.input)
			if valid != test.valid {
				t.Fatalf("parseAudioEventLine(%q) valid = %v, want %v", test.input, valid, test.valid)
			}
			if valid && got != test.want {
				t.Errorf("parseAudioEventLine(%q) = %#v, want %#v", test.input, got, test.want)
			}
		})
	}
}
