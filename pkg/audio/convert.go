package audio

import (
	"fmt"
	"log/slog"
	"sync"
)

// Format describes the sample rate and channel count of an audio stream.
type Format struct {
	SampleRate int
	Channels   int
}

// FormatConverter converts AudioFrames to a target format. It logs a warning
// on the first format mismatch and validates PCM data alignment.
// Create one per stream; not designed for shared use across goroutines.
type FormatConverter struct {
	Target         Format
	warnedMismatch sync.Once
	warnedCorrupt  sync.Once
}

// Convert converts a frame to the target format. If the source format already
// matches the target, the frame is returned unchanged (zero allocation).
// Conversion order: resample first, then channel convert.
func (c *FormatConverter) Convert(frame AudioFrame) AudioFrame {
	// Validate: odd byte count for int16 PCM.
	if len(frame.Data)%2 != 0 {
		c.warnedCorrupt.Do(func() {
			slog.Warn("audio format converter: odd byte count in PCM data, dropping frame",
				"bytes", len(frame.Data),
				"sampleRate", frame.SampleRate,
				"channels", frame.Channels,
			)
		})
		return AudioFrame{
			Data:       nil,
			SampleRate: c.Target.SampleRate,
			Channels:   c.Target.Channels,
			Timestamp:  frame.Timestamp,
		}
	}

	// Fast path: source matches target.
	if frame.SampleRate == c.Target.SampleRate && frame.Channels == c.Target.Channels {
		return frame
	}

	// Log warning on first mismatch.
	c.warnedMismatch.Do(func() {
		slog.Warn("audio format mismatch: converting",
			"from", formatString(frame.SampleRate, frame.Channels),
			"to", formatString(c.Target.SampleRate, c.Target.Channels),
		)
	})

	pcm := frame.Data
	currentRate := frame.SampleRate
	currentChannels := frame.Channels

	// Step 1: Resample first (avoids resampling stereo when target is mono).
	if currentRate != c.Target.SampleRate {
		if currentChannels == 1 {
			pcm = ResampleMono16(pcm, currentRate, c.Target.SampleRate)
		} else {
			pcm = ResampleStereo16(pcm, currentRate, c.Target.SampleRate)
		}
		currentRate = c.Target.SampleRate
	}

	// Step 2: Channel conversion.
	if currentChannels != c.Target.Channels {
		if currentChannels == 1 && c.Target.Channels == 2 {
			pcm = MonoToStereo(pcm)
		} else if currentChannels == 2 && c.Target.Channels == 1 {
			pcm = StereoToMono(pcm)
		}
		currentChannels = c.Target.Channels
	}

	return AudioFrame{
		Data:       pcm,
		SampleRate: currentRate,
		Channels:   currentChannels,
		Timestamp:  frame.Timestamp,
	}
}

// ConvertStream wraps an input channel with a conversion goroutine. It closes
// the returned channel when in closes. Uses cap(in) for the output channel
// buffer. Frames with empty data (e.g. from odd byte count) are dropped.
func ConvertStream(in <-chan AudioFrame, target Format) <-chan AudioFrame {
	out := make(chan AudioFrame, cap(in))
	go func() {
		defer close(out)
		conv := FormatConverter{Target: target}
		for frame := range in {
			converted := conv.Convert(frame)
			if len(converted.Data) == 0 {
				continue
			}
			out <- converted
		}
	}()
	return out
}

// MonoToStereo duplicates each int16 mono sample into a stereo L+R pair.
// Input must be little-endian int16 PCM (2 bytes per sample).
func MonoToStereo(pcm []byte) []byte {
	out := make([]byte, (len(pcm)/2)*4)
	for i := 0; i+1 < len(pcm); i += 2 {
		lo, hi := pcm[i], pcm[i+1]
		j := i * 2
		out[j] = lo
		out[j+1] = hi
		out[j+2] = lo
		out[j+3] = hi
	}
	return out
}

// StereoToMono averages L+R per stereo frame (4 bytes) to produce mono output.
// Uses int32 arithmetic to prevent overflow and clamps to int16 range.
func StereoToMono(pcm []byte) []byte {
	// Each stereo frame is 4 bytes (2 bytes L + 2 bytes R).
	frames := len(pcm) / 4
	out := make([]byte, frames*2)
	for i := range frames {
		lSample := int32(int16(pcm[i*4]) | int16(pcm[i*4+1])<<8)
		rSample := int32(int16(pcm[i*4+2]) | int16(pcm[i*4+3])<<8)
		avg := (lSample + rSample) / 2

		// Clamp to int16 range.
		if avg > 32767 {
			avg = 32767
		} else if avg < -32768 {
			avg = -32768
		}

		out[i*2] = byte(avg)
		out[i*2+1] = byte(avg >> 8)
	}
	return out
}

// ResampleMono16 resamples 16-bit mono PCM from srcRate to dstRate using linear
// interpolation. The input must be little-endian int16 samples. If srcRate ==
// dstRate, the input is returned unchanged.
func ResampleMono16(pcm []byte, srcRate, dstRate int) []byte {
	if srcRate <= 0 || dstRate <= 0 {
		return pcm
	}
	if srcRate == dstRate || len(pcm) < 2 {
		return pcm
	}
	srcSamples := len(pcm) / 2
	dstSamples := int(int64(srcSamples) * int64(dstRate) / int64(srcRate))
	if dstSamples == 0 {
		return nil
	}

	out := make([]byte, dstSamples*2)
	ratio := float64(srcRate) / float64(dstRate)

	for i := range dstSamples {
		srcPos := float64(i) * ratio
		srcIdx := int(srcPos)
		frac := srcPos - float64(srcIdx)

		s0 := int16(pcm[srcIdx*2]) | int16(pcm[srcIdx*2+1])<<8
		var s1 int16
		if srcIdx+1 < srcSamples {
			s1 = int16(pcm[(srcIdx+1)*2]) | int16(pcm[(srcIdx+1)*2+1])<<8
		} else {
			s1 = s0
		}

		interpolated := int16(float64(s0)*(1-frac) + float64(s1)*frac)
		out[i*2] = byte(interpolated)
		out[i*2+1] = byte(interpolated >> 8)
	}
	return out
}

// ResampleStereo16 resamples 16-bit stereo PCM from srcRate to dstRate using
// linear interpolation. Each stereo frame is 4 bytes (L+R interleaved).
// If srcRate == dstRate, the input is returned unchanged.
func ResampleStereo16(pcm []byte, srcRate, dstRate int) []byte {
	if srcRate <= 0 || dstRate <= 0 {
		return pcm
	}
	if srcRate == dstRate || len(pcm) < 4 {
		return pcm
	}
	srcFrames := len(pcm) / 4
	dstFrames := int(int64(srcFrames) * int64(dstRate) / int64(srcRate))
	if dstFrames == 0 {
		return nil
	}

	out := make([]byte, dstFrames*4)
	ratio := float64(srcRate) / float64(dstRate)

	for i := range dstFrames {
		srcPos := float64(i) * ratio
		srcIdx := int(srcPos)
		frac := srcPos - float64(srcIdx)

		// Left channel
		l0 := int16(pcm[srcIdx*4]) | int16(pcm[srcIdx*4+1])<<8
		// Right channel
		r0 := int16(pcm[srcIdx*4+2]) | int16(pcm[srcIdx*4+3])<<8

		var l1, r1 int16
		if srcIdx+1 < srcFrames {
			l1 = int16(pcm[(srcIdx+1)*4]) | int16(pcm[(srcIdx+1)*4+1])<<8
			r1 = int16(pcm[(srcIdx+1)*4+2]) | int16(pcm[(srcIdx+1)*4+3])<<8
		} else {
			l1 = l0
			r1 = r0
		}

		lInterp := int16(float64(l0)*(1-frac) + float64(l1)*frac)
		rInterp := int16(float64(r0)*(1-frac) + float64(r1)*frac)

		out[i*4] = byte(lInterp)
		out[i*4+1] = byte(lInterp >> 8)
		out[i*4+2] = byte(rInterp)
		out[i*4+3] = byte(rInterp >> 8)
	}
	return out
}

// formatString returns a human-readable string for a sample rate and channel count,
// e.g. "48000Hz stereo".
func formatString(rate, channels int) string {
	ch := "mono"
	if channels == 2 {
		ch = "stereo"
	} else if channels > 2 {
		ch = fmt.Sprintf("%dch", channels)
	}
	return fmt.Sprintf("%dHz %s", rate, ch)
}
