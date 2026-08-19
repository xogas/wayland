package shared

import (
	"fmt"
	"time"
)

// ReportFPS prints the frame count and the average frame rate since start.
func ReportFPS(start time.Time, frames int) {
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		return
	}
	fmt.Printf("%d frames in %.1fs (%.1f fps)\n", frames, elapsed, float64(frames)/elapsed)
}
