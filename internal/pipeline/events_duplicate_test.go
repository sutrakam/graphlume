package pipeline

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// A Kafka topic literal on the same line as kafka.Writer{...} is matched by
// two patterns: the kafka-go writer pattern and the producer context's Topic
// field. The extractor reported it twice, the second PUBLISHES edge collided
// with the first on the edges table's unique key, and the whole file was
// recorded as failed: its functions, calls and imports gone from the index.
func TestKafkaWriterTopicOnOneLineIndexesTheFileOnce(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"producer.go": `package producer

import "github.com/segmentio/kafka-go"

func NewOrderWriter() *kafka.Writer {
	return &kafka.Writer{Addr: kafka.TCP("localhost:9092"), Topic: "orders.created"}
}

func Helper() int { return 1 }
`,
	})
	reg, runID, _ := indexTree(t, root, filepath.Join(t.TempDir(), "index.db"))

	files, err := reg.Files(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if f := files[0]; f.ParseStatus == model.ParseStatusFailed {
		t.Fatalf("the file was marked failed: %s", f.ParseError)
	}

	edges, err := reg.Edges(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	pub := 0
	for _, e := range edges {
		if e.Kind == model.EdgePublishes {
			pub++
		}
	}
	if pub != 1 {
		t.Errorf("want exactly 1 PUBLISHES edge, got %d", pub)
	}

	report, err := reg.EventTopologyReport(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(report) != 1 || len(report[0].Producers) != 1 || report[0].Producers[0].SymbolName != "NewOrderWriter" {
		t.Errorf("want one topic produced once by NewOrderWriter, got %+v", report)
	}
}
