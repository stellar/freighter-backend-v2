package logger

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFields_SetAppendsInOrder(t *testing.T) {
	f := NewFields()
	f.Set("user_id", "abc")
	f.Set("iss", "freighter-extension")
	assert.Equal(t, []any{"user_id", "abc", "iss", "freighter-extension"}, f.Args())
}

func TestFields_NilReceiverIsSafe(t *testing.T) {
	var f *Fields
	assert.NotPanics(t, func() {
		f.Set("k", "v")
		assert.Nil(t, f.Args())
	})
}

func TestFields_ContextRoundTrip(t *testing.T) {
	f := NewFields()
	ctx := ContextWithFields(context.Background(), f)
	assert.Same(t, f, FieldsFromContext(ctx))
}

func TestFields_ContextAbsentReturnsNilSafeHolder(t *testing.T) {
	got := FieldsFromContext(context.Background())
	assert.Nil(t, got)
	assert.NotPanics(t, func() { got.Set("k", "v") })
}
