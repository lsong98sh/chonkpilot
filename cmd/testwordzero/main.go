package main

import (
	"fmt"
	"os"

	"github.com/jacoblai/wordZero/pkg/markdown"
)

func main() {
	docxPath := `e:\GoDev\1.1可行性研究报告.docx`
	outputPath := `e:\GoDev\output_wordzero.md`

	exporter := markdown.NewExporter(markdown.HighQualityExportOptions())
	err := exporter.ExportToFile(docxPath, outputPath, nil)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	data, _ := os.ReadFile(outputPath)
	fmt.Printf("Output: %d bytes\n", len(data))
	fmt.Println("===== First 2000 chars =====")
	fmt.Println(string(data[:min(2000, len(data))]))
	fmt.Println("\n===== Last 500 chars (tables area) =====")
	total := len(data)
	start := total - 500
	if start < 0 {
		start = 0
	}
	fmt.Println(string(data[start:]))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
