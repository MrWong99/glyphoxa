package discord

import (
	"fmt"

	"layeh.com/gopus"
)

// Discord voice uses 48 kHz stereo Opus at 20 ms frame size.
const (
	opusSampleRate  = 48000
	opusChannels    = 2
	opusFrameSizeMs = 20
	// opusFrameSize is the number of samples per channel per 20 ms frame.
	opusFrameSize = opusSampleRate * opusFrameSizeMs / 1000 // 960
)

// opusDecoder wraps a gopus Opus decoder for a single participant stream.
// Each participant gets its own decoder to maintain decoder state correctly
// across consecutive frames.
type opusDecoder struct {
	dec *gopus.Decoder
}

// newOpusDecoder creates a new Opus decoder configured for Discord audio.
func newOpusDecoder() (*opusDecoder, error) {
	dec, err := gopus.NewDecoder(opusSampleRate, opusChannels)
	if err != nil {
		return nil, fmt.Errorf("discord: create opus decoder: %w", err)
	}
	return &opusDecoder{dec: dec}, nil
}

// decode decodes an Opus packet into interleaved PCM int16 samples and returns
// the result as a byte slice (little-endian int16 pairs).
func (d *opusDecoder) decode(opus []byte) ([]byte, error) {
	pcm, err := d.dec.Decode(opus, opusFrameSize, false)
	if err != nil {
		return nil, fmt.Errorf("discord: opus decode: %w", err)
	}
	return int16sToBytes(pcm), nil
}

// opusEncoder wraps a gopus Opus encoder for the output stream.
type opusEncoder struct {
	enc *gopus.Encoder
}

// newOpusEncoder creates a new Opus encoder configured for Discord audio.
func newOpusEncoder() (*opusEncoder, error) {
	enc, err := gopus.NewEncoder(opusSampleRate, opusChannels, gopus.Audio)
	if err != nil {
		return nil, fmt.Errorf("discord: create opus encoder: %w", err)
	}
	return &opusEncoder{enc: enc}, nil
}

// encode encodes interleaved PCM int16 data (as bytes, little-endian) into an Opus packet.
func (e *opusEncoder) encode(pcmBytes []byte) ([]byte, error) {
	pcm := bytesToInt16s(pcmBytes)
	opus, err := e.enc.Encode(pcm, opusFrameSize, len(pcmBytes))
	if err != nil {
		return nil, fmt.Errorf("discord: opus encode: %w", err)
	}
	return opus, nil
}

// int16sToBytes converts a slice of int16 PCM samples to little-endian bytes.
func int16sToBytes(pcm []int16) []byte {
	b := make([]byte, len(pcm)*2)
	for i, s := range pcm {
		b[i*2] = byte(s)
		b[i*2+1] = byte(s >> 8)
	}
	return b
}

// bytesToInt16s converts little-endian bytes to a slice of int16 PCM samples.
func bytesToInt16s(b []byte) []int16 {
	pcm := make([]int16, len(b)/2)
	for i := range pcm {
		pcm[i] = int16(b[i*2]) | int16(b[i*2+1])<<8
	}
	return pcm
}
