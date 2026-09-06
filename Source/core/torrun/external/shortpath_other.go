//go:build !windows

package external

// путьДляTor на остальных системах ничего не меняет: tor там читает пути
// в UTF-8 и понимает их как есть.
func путьДляTor(путь string) string { return путь }
