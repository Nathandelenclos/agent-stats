package main

import (
	"encoding/json"
	"testing"
)

func TestPriceJSONKeepsPaidTextModels(t *testing.T) {
	source := map[string]json.RawMessage{
		"text":  json.RawMessage(`{"mode":"chat","input_cost_per_token":0.01,"output_cost_per_token":0.02}`),
		"image": json.RawMessage(`{"mode":"image_generation","input_cost_per_token":0.1}`),
	}
	got, err := priceJSON(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{\n \"text\":[0.01,0.02,0,0]\n}\n" {
		t.Fatalf("unexpected generated prices: %s", got)
	}
}

func TestPriceJSONRejectsAnUnusableSource(t *testing.T) {
	if _, err := priceJSON(nil); err == nil {
		t.Fatal("empty price source accepted")
	}
}
