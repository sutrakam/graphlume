package notify

import (
	"context"
	"testing"
)

type recorder struct{ subjects []string }

func (r *recorder) Send(_ context.Context, subject, _ string) error {
	r.subjects = append(r.subjects, subject)
	return nil
}

func TestHandle_SendsReceipt(t *testing.T) {
	rec := &recorder{}
	err := NewConsumer(rec).Handle(context.Background(),
		[]byte(`{"id":"pay_1","amount":{"cents":500,"currency":"EUR"},"status":"captured"}`))
	if err != nil || len(rec.subjects) != 1 {
		t.Fatalf("err=%v subjects=%v", err, rec.subjects)
	}
}
