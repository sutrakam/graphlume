package export

import (
	"context"
	"strings"
	"testing"

	regpkg "github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/store"
	"github.com/sutrakam/graphlume/pkg/model"
)

func TestBuildASCIITree(t *testing.T) {
	paths := []string{
		"cmd/main.go",
		"internal/api/routes.go",
		"internal/api/server.go",
		"README.md",
	}

	tree := BuildASCIITree(paths)
	if !strings.Contains(tree, "cmd/") {
		t.Errorf("expected tree to contain cmd/, got:\n%s", tree)
	}
	if !strings.Contains(tree, "internal/") {
		t.Errorf("expected tree to contain internal/, got:\n%s", tree)
	}
	if !strings.Contains(tree, "README.md") {
		t.Errorf("expected tree to contain README.md, got:\n%s", tree)
	}
}

func TestGeneratePack(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	reg := regpkg.New(db)
	run, err := reg.BeginRun(ctx, "fast", "")
	if err != nil {
		t.Fatal(err)
	}
	w, err := reg.NewWriter(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}

	file1 := &model.File{
		Path:        "src/auth.go",
		ParseStatus: model.ParseStatusOK,
		SizeBytes:   100,
	}
	content1 := "package auth\nconst Secret = \"sk_live_1234567890abcdef\"\n"
	fileID, err := w.AddFile(ctx, file1)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.IndexFileText(ctx, fileID, file1.Path, content1); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// 1. XML pack with redaction
	resXML, err := GeneratePack(ctx, reg, run.ID, PackOptions{
		Format:      PackFormatXML,
		IncludeTree: true,
		Redact:      true,
	})
	if err != nil {
		t.Fatalf("GeneratePack XML: %v", err)
	}
	if resXML.FileCount != 1 {
		t.Errorf("expected 1 file, got %d", resXML.FileCount)
	}
	if strings.Contains(resXML.Content, "sk_live_") {
		t.Errorf("expected API key to be redacted, got:\n%s", resXML.Content)
	}
	if !strings.Contains(resXML.Content, "<graphlume_pack>") {
		t.Errorf("expected XML root element, got:\n%s", resXML.Content)
	}

	// 2. Markdown pack
	resMD, err := GeneratePack(ctx, reg, run.ID, PackOptions{
		Format:      PackFormatMarkdown,
		IncludeTree: true,
		Redact:      true,
	})
	if err != nil {
		t.Fatalf("GeneratePack MD: %v", err)
	}
	if !strings.Contains(resMD.Content, "# Codebase Context Pack") {
		t.Errorf("expected Markdown header, got:\n%s", resMD.Content)
	}

	// 3. JSON pack
	resJSON, err := GeneratePack(ctx, reg, run.ID, PackOptions{
		Format:      PackFormatJSON,
		IncludeTree: true,
		Redact:      true,
	})
	if err != nil {
		t.Fatalf("GeneratePack JSON: %v", err)
	}
	if !strings.Contains(resJSON.Content, "\"files\"") || !strings.Contains(resJSON.Content, "src/auth.go") {
		t.Errorf("expected valid JSON pack, got:\n%s", resJSON.Content)
	}

	// 4. StreamPack to io.Writer directly
	var buf strings.Builder
	resStream, err := StreamPack(ctx, reg, run.ID, PackOptions{
		Format:      PackFormatXML,
		IncludeTree: true,
		Redact:      true,
	}, &buf)
	if err != nil {
		t.Fatalf("StreamPack: %v", err)
	}
	if resStream.FileCount != 1 || resStream.RedactedCount != 1 {
		t.Errorf("unexpected StreamPack result: %+v", resStream)
	}
	if buf.String() != resXML.Content {
		t.Errorf("StreamPack and GeneratePack XML divergence:\nStream:\n%s\nGen:\n%s", buf.String(), resXML.Content)
	}
}
