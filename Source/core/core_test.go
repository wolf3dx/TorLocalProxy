package core

import (
	"strings"
	"testing"
)

func TestBannerСодержитИмяВерсиюИКоммит(t *testing.T) {
	got := Banner()
	for _, часть := range []string{Name, Version, Commit} {
		if !strings.Contains(got, часть) {
			t.Errorf("Banner() = %q, нет составляющей %q", got, часть)
		}
	}
}
