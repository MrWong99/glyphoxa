package whisper

import "encoding/binary"

// pcmToFloat32 converts 16-bit signed little-endian PCM audio to float32
// samples normalised to the range [-1.0, 1.0]. The input length must be
// even (two bytes per sample); any trailing odd byte is silently ignored.
func pcmToFloat32(pcm []byte) []float32 {
	n := len(pcm) / 2
	samples := make([]float32, n)
	for i := range n {
		sample := int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2]))
		samples[i] = float32(sample) / 32768.0
	}
	return samples
}

// pcmToFloat32Mono down-mixes multi-channel 16-bit PCM to mono float32 by
// averaging all channels per frame. If channels is 1 this is equivalent to
// pcmToFloat32.
func pcmToFloat32Mono(pcm []byte, channels int) []float32 {
	if channels <= 1 {
		return pcmToFloat32(pcm)
	}
	samplesPerChannel := len(pcm) / (2 * channels)
	mono := make([]float32, samplesPerChannel)
	for i := range samplesPerChannel {
		var sum float32
		for ch := range channels {
			idx := (i*channels + ch) * 2
			sample := int16(binary.LittleEndian.Uint16(pcm[idx : idx+2]))
			sum += float32(sample) / 32768.0
		}
		mono[i] = sum / float32(channels)
	}
	return mono
}
