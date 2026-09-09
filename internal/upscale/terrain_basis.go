package upscale

// The feature basis: the PCA subspace fitted to the example blocks and the
// per-palette-index contributions the quantizer uses. See README.md.

import (
	"math"
)

// makePCABasis fits the neighborhood basis to sampled 5x5 windows of the
// reduced examples. Each sample first draws a placement uniformly, so the
// basis reflects the map as laid out rather than its unique tile set: fitted
// over unique tiles the rare high-contrast graphics dominate and the retained
// dimensions stop discriminating the common smooth textures, which raised the
// share of high-contrast output blocks from 35% to 49% on Great Divide.
func makePCABasis(db database, atlas tileAtlas, palette []byte, samples, workers int) []float32 {
	positions := make([]int, samples)
	interior := sourceTileSize / 2 // block-aligned low positions per tile axis
	for sample := range samples {
		random := splitmix64(uint64(sample) + 0x8d58ac26afe12e47)
		tile := atlas.tileMap[random%uint64(len(atlas.tileMap))]
		random = splitmix64(random)
		phase := int(random & 3)
		localY := int((random >> 8) % uint64(interior))
		localX := int((random >> 32) % uint64(interior))
		cellY, cellX := (tile/atlas.columns)*cellSize, (tile%atlas.columns)*cellSize
		lowY := (cellY+haloSize-phase/2)/2 + localY
		lowX := (cellX+haloSize-phase%2)/2 + localX
		center := (phase*db.phaseHeight+lowY)*db.width + lowX
		positions[sample] = center - patchRadius*db.width - patchRadius
	}
	partials := make([][]float64, workers)
	parallelIndexed(samples, workers, func(worker, begin, end int) {
		local := make([]float64, patchValues*patchValues)
		var values [patchValues]float64
		for _, position := range positions[begin:end] {
			originY, originX := position/db.width, position%db.width
			component := 0
			for y := 0; y < patchSpan; y++ {
				row := (originY+y)*db.width + originX
				for x := 0; x < patchSpan; x++ {
					paletteIndex := int(db.low[row+x]) * 3
					values[component] = float64(palette[paletteIndex])
					values[component+1] = float64(palette[paletteIndex+1])
					values[component+2] = float64(palette[paletteIndex+2])
					component += 3
				}
			}
			for left, leftValue := range values {
				base := left * patchValues
				for right := 0; right <= left; right++ {
					local[base+right] += leftValue * values[right]
				}
			}
		}
		partials[worker] = local
	})
	covariance := make([]float64, patchValues*patchValues)
	for _, partial := range partials {
		for left := range patchValues {
			for right := 0; right <= left; right++ {
				covariance[left*patchValues+right] += partial[left*patchValues+right]
			}
		}
	}
	for left := range patchValues {
		for right := 0; right < left; right++ {
			covariance[right*patchValues+left] = covariance[left*patchValues+right]
		}
	}
	return dominantSubspace(covariance, featureDims)
}

func dominantSubspace(covariance []float64, dimensions int) []float32 {
	vectors := make([]float64, dimensions*patchValues)
	for dimension := range dimensions {
		for component := range patchValues {
			random := splitmix64(uint64(dimension*patchValues+component) + 0xb6c8e9cf570932bd)
			vectors[dimension*patchValues+component] = float64(int64(random>>11)&0xfffff)/524288 - 1
		}
	}
	orthonormalize(vectors, dimensions)
	next := make([]float64, len(vectors))
	for range 32 {
		for dimension := range dimensions {
			input := vectors[dimension*patchValues : (dimension+1)*patchValues]
			output := next[dimension*patchValues : (dimension+1)*patchValues]
			for row := range patchValues {
				sum := 0.0
				base := row * patchValues
				for column, value := range input {
					sum += covariance[base+column] * value
				}
				output[row] = sum
			}
		}
		orthonormalize(next, dimensions)
		vectors, next = next, vectors
	}
	result := make([]float32, len(vectors))
	for index, value := range vectors {
		result[index] = float32(value)
	}
	return result
}

func orthonormalize(vectors []float64, dimensions int) {
	for current := range dimensions {
		vector := vectors[current*patchValues : (current+1)*patchValues]
		for prior := 0; prior < current; prior++ {
			other := vectors[prior*patchValues : (prior+1)*patchValues]
			dot := 0.0
			for index, value := range vector {
				dot += value * other[index]
			}
			for index := range vector {
				vector[index] -= dot * other[index]
			}
		}
		norm := 0.0
		for _, value := range vector {
			norm += value * value
		}
		norm = math.Sqrt(norm)
		if norm < 1e-20 {
			clear(vector)
			vector[current%patchValues] = 1
			continue
		}
		for index := range vector {
			vector[index] /= norm
		}
	}
}

func makeContributions(basis []float32, palette []byte) []float32 {
	dimensions := featureDims
	result := make([]float32, patchSpan*patchSpan*paletteSize*dimensions)
	for patchPosition := range patchSpan * patchSpan {
		component := patchPosition * 3
		for paletteIndex := range paletteSize {
			red := float32(palette[paletteIndex*3])
			green := float32(palette[paletteIndex*3+1])
			blue := float32(palette[paletteIndex*3+2])
			output := (patchPosition*paletteSize + paletteIndex) * dimensions
			for dimension := range dimensions {
				weight := dimension * patchValues
				result[output+dimension] = red*basis[weight+component] +
					green*basis[weight+component+1] + blue*basis[weight+component+2]
			}
		}
	}
	return result
}
