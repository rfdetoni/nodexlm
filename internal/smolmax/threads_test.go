package smolmax

import (
    "os"
    "path/filepath"
    "strconv"
    "testing"
)

func TestBigLittleTopology(t *testing.T) {
    root := t.TempDir()
    for i := 0; i < 8; i++ {
        dir := filepath.Join(root, "cpu"+strconv.Itoa(i), "cpufreq")
        if err := os.MkdirAll(dir, 0755); err != nil { t.Fatal(err) }
        freq := "1800000"
        if i < 2 { freq = "2600000" }
        if err := os.WriteFile(filepath.Join(dir, "cpuinfo_max_freq"), []byte(freq), 0644); err != nil { t.Fatal(err) }
    }
    if n := RecommendedThreads(root, 8); n != 2 { t.Fatalf("expected two performance cores, got %d", n) }
    if n := RecommendedThreads(root, 12); n != 4 { t.Fatalf("incomplete topology should fall back, got %d", n) }
}
func TestThreadPolicy(t *testing.T) {
    if n, err := Threads(6, ""); err != nil || n != 6 { t.Fatalf("%d %v", n, err) }
    if n, err := Threads(0, ""); err != nil || n != 4 { t.Fatalf("%d %v", n, err) }
    if _, err := Threads(4, "unknown"); err == nil { t.Fatal("accepted invalid policy") }
}
