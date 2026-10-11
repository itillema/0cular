package main

import "testing"

func TestBuildInfoString(t *testing.T) {
	tests := []struct {
		name string
		info buildInfo
		want string
	}{
		{
			name: "release build",
			info: buildInfo{
				version: "v0.1.0", commit: "3d3c42e5", date: "2026-10-10T12:00:00Z",
				goVersion: "go1.27.2", platform: "linux/amd64",
			},
			want: "0cular v0.1.0\n" +
				"commit:   3d3c42e5\n" +
				"date:     2026-10-10T12:00:00Z\n" +
				"go:       go1.27.2\n" +
				"platform: linux/amd64\n",
		},
		{
			name: "build from a modified checkout",
			info: buildInfo{
				version: "(devel)", commit: "3d3c42e5", modified: true,
				goVersion: "go1.27.2", platform: "windows/amd64",
			},
			want: "0cular (devel)\n" +
				"commit:   3d3c42e5 (modified)\n" +
				"go:       go1.27.2\n" +
				"platform: windows/amd64\n",
		},
		{
			name: "build without version control information",
			info: buildInfo{version: "(devel)", goVersion: "go1.26.9", platform: "darwin/arm64"},
			want: "0cular (devel)\n" +
				"go:       go1.26.9\n" +
				"platform: darwin/arm64\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.info.String(); got != tt.want {
				t.Errorf("String() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestCurrentBuildInfoPrefersLinkTimeValues(t *testing.T) {
	version, commit, date = "v9.9.9", "abc123", "2026-10-10T00:00:00Z"
	t.Cleanup(func() { version, commit, date = "", "", "" })

	b := currentBuildInfo()
	if b.version != "v9.9.9" || b.commit != "abc123" || b.date != "2026-10-10T00:00:00Z" {
		t.Errorf("currentBuildInfo() = %+v, want the link-time values", b)
	}
	if b.goVersion == "" || b.platform == "" {
		t.Errorf("currentBuildInfo() = %+v, want the Go version and platform filled in", b)
	}
}
