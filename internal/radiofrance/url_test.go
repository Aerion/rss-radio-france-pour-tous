package radiofrance

import "testing"

func TestIsPlayableAudioManifestation(t *testing.T) {
	tests := []struct {
		mediaType string
		want      bool
	}{
		{"mp3", true},
		{"", true},
		{"youtube", false},
		{"YouTube", false},
		{"dailymotion", false},
		{"DailyMotion", false},
	}
	for _, tt := range tests {
		if got := IsPlayableAudioManifestation(tt.mediaType); got != tt.want {
			t.Errorf("IsPlayableAudioManifestation(%q) = %v, want %v", tt.mediaType, got, tt.want)
		}
	}
}

func TestIsPlayableAudioURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://media.radiofrance-podcast.net/podcast09/foo.m4a", true},
		{"https://proxycast.radiofrance.fr/abc/foo.mp3", true},
		{"https://cdn.example.com/audio.mp3", true},
		{"https://www.youtube.com/watch?v=xyz", false},
		{"https://youtube.com/watch?v=xyz", false},
		{"https://m.youtube.com/watch?v=xyz", false},
		{"https://youtu.be/xyz", false},
		{"https://www.dailymotion.com/video/xyz", false},
		{"https://dailymotion.com/video/xyz", false},
		{"https://notyoutube.com/watch?v=xyz", true},
		{"https://youtube.com.evil.example/watch?v=xyz", true},
		{"not a url", true},
		{"", true},
	}
	for _, tt := range tests {
		if got := IsPlayableAudioURL(tt.url); got != tt.want {
			t.Errorf("IsPlayableAudioURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
