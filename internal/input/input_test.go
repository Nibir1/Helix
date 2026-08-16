package input

import "testing"

func TestChannelValidity(t *testing.T) {
	if !ChannelText.Valid() {
		t.Error("ChannelText must be valid")
	}
	if !ChannelVoice.Valid() {
		t.Error("ChannelVoice must be valid")
	}
	if Channel("carrier-pigeon").Valid() {
		t.Error("unknown channel must be invalid")
	}
}
