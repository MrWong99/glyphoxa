package audio

// Drain reads from ch until the channel is closed, discarding all values.
// Use this to prevent goroutine leaks when you don't need the data from a
// streaming channel (e.g., an Audio channel on [AudioSegment] or
// engine.Response).
func Drain[T any](ch <-chan T) {
	for range ch {
	}
}
