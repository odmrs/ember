package credentials

import "testing"

func TestExplicitAccountConfiguration(t *testing.T) {
	t.Setenv("ANNAS_ARCHIVE_KEY", "fake-account-key")
	t.Setenv("BOOKGET_ANNA_URL", "https://annas-archive.gl")
	if Load() != "fake-account-key" || Base() != "https://annas-archive.gl" {
		t.Fatal("explicit configuration ignored")
	}
}
