package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
)

func TestCommunityFooterOptionsProjectVeteranPreference(t *testing.T) {
	cl, err := client.New(client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := communityFooterOptions(cl); got.VeteranLabel {
		t.Fatalf("zero-value client projected VeteranLabel=true")
	}
	cl.SetCommunityHUDOptions(client.CommunityHUDOptions{VeteranLabel: true})
	if got := communityFooterOptions(cl); !got.VeteranLabel {
		t.Fatalf("enabled client projected VeteranLabel=false")
	}
}
