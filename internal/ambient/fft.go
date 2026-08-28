// internal/ambient/fft.go
// Purpose: A minimal, dependency-free iterative radix-2 FFT (Cooley–Tukey)
// for the ambient monitor's crude spectral features (BlackBox Phase 6). The
// roadmap allowed "hand-rolled FFT or go-dsp if license-clean" — hand-rolled
// keeps the CGO-free, near-zero-dependency build.
package ambient

import "math"

// nextPow2 returns the smallest power of two >= n.
func nextPow2(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// fftInPlace computes the in-place complex FFT of re/im (length must be a
// power of two). The sign convention matches a forward DFT; only magnitudes
// are consumed downstream, so normalization is irrelevant.
func fftInPlace(re, im []float64) {
	n := len(re)

	// Bit-reversal permutation.
	for i, j := 0, 0; i < n; i++ {
		if j > i {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
		m := n >> 1
		for m >= 1 && j >= m {
			j -= m
			m >>= 1
		}
		j += m
	}

	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		angle := -2 * math.Pi / float64(size)
		wRe := math.Cos(angle)
		wIm := math.Sin(angle)
		for i := 0; i < n; i += size {
			curRe, curIm := 1.0, 0.0
			for j := 0; j < half; j++ {
				a := i + j
				b := a + half
				tRe := curRe*re[b] - curIm*im[b]
				tIm := curRe*im[b] + curIm*re[b]
				re[b] = re[a] - tRe
				im[b] = im[a] - tIm
				re[a] += tRe
				im[a] += tIm

				nxtRe := curRe*wRe - curIm*wIm
				curIm = curRe*wIm + curIm*wRe
				curRe = nxtRe
			}
		}
	}
}

// magnitudeSpectrum returns the magnitude spectrum for bins 1..n/2-1 (DC and
// Nyquist excluded) from an input window of arbitrary length (zero-padded to
// the next power of two).
func magnitudeSpectrum(samples []float64) []float64 {
	n := nextPow2(len(samples))
	re := make([]float64, n)
	im := make([]float64, n)
	copy(re, samples)
	fftInPlace(re, im)

	mag := make([]float64, 0, n/2-1)
	for i := 1; i < n/2; i++ {
		mag = append(mag, math.Hypot(re[i], im[i]))
	}
	return mag
}
