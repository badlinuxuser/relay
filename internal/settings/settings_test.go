package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := Open(dir)
	d := st.Get()
	if !d.ShowThoughts || d.StreamSpeed != "normal" || !d.Mouse || !d.Mascot || d.Sidebar != "auto" {
		t.Fatalf("defaults: %+v", d)
	}
	d.ShowThoughts = false
	d.StreamSpeed = "bogus"
	d.TextWidth = 5
	d.ProcessArgs = map[string]string{"llamacpp": "-m /m/q.gguf", "x": "  "}
	d.Autostart = map[string]bool{"ollama": true}
	got, err := st.Set(d)
	if err != nil {
		t.Fatal(err)
	}
	if got.StreamSpeed != "normal" || got.TextWidth != 40 || len(got.ProcessArgs) != 1 {
		t.Errorf("cleaned: %+v", got)
	}
	again := Open(dir).Get()
	if again.ShowThoughts || again.ProcessArgs["llamacpp"] != "-m /m/q.gguf" || !again.Autostart["ollama"] {
		t.Errorf("not persisted: %+v", again)
	}

	// a file with only some fields keeps the defaults for the others
	_ = os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"stream_speed":"fast"}`), 0o600)
	p := Open(dir).Get()
	if p.StreamSpeed != "fast" || !p.ShowThoughts || !p.Mouse {
		t.Errorf("partial file: %+v", p)
	}
	// a broken file gives the defaults
	_ = os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{broken`), 0o600)
	if !Open(dir).Get().ShowThoughts {
		t.Error("broken file")
	}
	if SpeedCPS("instant") != 0 || SpeedCPS("slow") >= SpeedCPS("normal") || SpeedCPS("normal") >= SpeedCPS("fast") {
		t.Error("speeds")
	}
}
