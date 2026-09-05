package main

// The PatchMatch search itself: query construction, propagation and random
// search, the parent relaxation pass and the cost summaries. See README.md.

import (
	"fmt"
)

func makeTileQueries(data dataset, atlas tileAtlas, contributions []float32, workers int) tileQueries {
	dimensions := featureDims
	mapWidth, mapHeight := atlas.mapWidth, atlas.mapHeight
	first, tileMap := atlas.firstPlacements, atlas.tileMap
	parents := make([]byte, len(first)*sourceTileSize*sourceTileSize)
	features := make([][featureDims]int8, len(parents))
	parallel(len(first), workers, func(begin, end int) {
		var sums [8]float32
		for identifier := begin; identifier < end; identifier++ {
			placement := first[identifier]
			tileY, tileX := placement/mapWidth, placement%mapWidth
			originY, originX := tileY*sourceTileSize, tileX*sourceTileSize
			for y := range sourceTileSize {
				for x := range sourceTileSize {
					query := identifier*sourceTileSize*sourceTileSize + y*sourceTileSize + x
					parents[query] = data.high[(originY+y)*data.metadata.Width+originX+x]
					clear(sums[:])
					patchPosition := 0
					for dy := -patchRadius; dy <= patchRadius; dy++ {
						sampleY := clamp(originY+y+dy, 0, data.metadata.Height-1)
						for dx := -patchRadius; dx <= patchRadius; dx++ {
							sampleX := clamp(originX+x+dx, 0, data.metadata.Width-1)
							index := int(data.high[sampleY*data.metadata.Width+sampleX])
							contribution := (patchPosition*paletteSize + index) * dimensions
							for dimension := range dimensions {
								sums[dimension] += contributions[contribution+dimension]
							}
							patchPosition++
						}
					}
					features[query] = quantizeFeatures(&sums)
				}
			}
		}
	})
	return tileQueries{
		parents: parents, features: features, firstPlacements: first, tileMap: tileMap,
		mapWidth: mapWidth, mapHeight: mapHeight,
	}
}

// patchMatch runs the classic sequential PatchMatch independently per unique
// tile: alternating raster scans propagate matches from the two already-visited
// neighbors, then each pixel tries a shrinking random window around its match
// plus two global draws. Every tile is self-contained, so the result does not
// depend on worker scheduling.
// patchMatch's tone term (options.tone > 0) penalizes the squared distance
// between a candidate block's RGB sum and four times its parent colour, so the
// 2x result averages back to the 1x map instead of inheriting the ALP blend's
// brightening and hue drift [03 §3.7]. Because for dark parents every authored
// block may be brighter than the parent, options.spread makes each candidate
// also offset a fraction of the mean error of its already-chosen 3x3
// neighbours, letting neighbouring pixels compensate for one another.
func patchMatch(queries tileQueries, db database, records []record, palette []byte, atlas tileAtlas, iterations int,
	options searchOptions, workers int) ([]int32, []int32, int) {
	toneWeight, spread, deadzone := options.tone, options.spread, options.deadzone
	sums := paletteSums(palette)
	var targets [paletteSize][3]int32
	for index := range paletteSize {
		for channel := range 3 {
			targets[index][channel] = 4 * int32(palette[index*3+channel])
		}
	}
	seamWeight, seamZone, coherenceWeight := options.seam, options.seamZone, options.coherence
	settle := options.settle
	var pairDistance []int32
	if seamWeight > 0 || coherenceWeight > 0 {
		pairDistance = make([]int32, paletteSize*paletteSize)
		for a := range paletteSize {
			for b := range paletteSize {
				var total int32
				for channel := range 3 {
					d := int32(palette[a*3+channel]) - int32(palette[b*3+channel])
					total += d * d
				}
				pairDistance[a*paletteSize+b] = total
			}
		}
	}
	const tilePixels = sourceTileSize * sourceTileSize
	// synthWidth is the tile's output width plus a two-pixel border of
	// "unknown" (0xff sentinel) so the coherence term can read a causal
	// neighbourhood without bounds checks.
	const synthWidth = outputTileSize + 4
	// Coherence neighbourhood: two rows above the block (six pixels each) and
	// two columns left (two pixels each) on forward passes; the mirror image
	// (below and right) on backward passes. Precomputed as flat index deltas
	// into the synthesized tile and into the atlas.
	var coherenceOffsets = [16][2]int{{-1, -2}, {-1, -1}, {-1, 0}, {-1, 1}, {-1, 2}, {-1, 3}, {-2, -2}, {-2, -1}, {-2, 0}, {-2, 1}, {-2, 2}, {-2, 3}, {0, -1}, {1, -1}, {0, -2}, {1, -2}}
	var synthDeltas, atlasDeltas [2][16]int
	for direction := range 2 {
		for index, offset := range coherenceOffsets {
			dy, dx := offset[0], offset[1]
			if direction == 1 {
				dy, dx = 1-dy, 1-dx
			}
			synthDeltas[direction][index] = dy*synthWidth + dx
			atlasDeltas[direction][index] = dy*atlas.width + dx
		}
	}
	n := len(queries.parents)
	tileCount := n / tilePixels
	matches := make([]int32, n)
	costs := make([]int32, n)

	parallel(tileCount, workers, func(begin, end int) {
		var candidates [8]int32
		var buckets [tilePixels]int32
		var chosen [tilePixels][3]int32 // RGB-sum error of each pixel's current block
		var chosenBlock [tilePixels][4]byte
		var synthesized [synthWidth * synthWidth]byte
		var unchanged [tilePixels]uint8 // consecutive passes the match survived
		atlasPositions := 4 * db.phaseHeight * db.width
		var featCosts [tilePixels]int32
		for tile := begin; tile < end; tile++ {
			base := tile * tilePixels
			parents := queries.parents[base : base+tilePixels : base+tilePixels]
			features := queries.features[base : base+tilePixels : base+tilePixels]
			tileMatches := matches[base : base+tilePixels : base+tilePixels]
			rng := splitmix64(uint64(tile) + 0x8d58ac26afe12e47)
			next := func() uint64 {
				rng = splitmix64(rng)
				return rng
			}
			// globalDraw returns a random valid database position with the
			// query's parent, from the feature-hash bucket when asked and the
			// bucket is populated, and -1 when no position exists.
			globalDraw := func(pixel int, bucketed bool) int32 {
				parent := int32(db.sole[parents[pixel]])
				if parent < 0 {
					parent = db.neighbours.draw(int(parents[pixel]), next())
					if parent < 0 {
						return -1
					}
				}
				if bucketed {
					bucket := int(parent)*hashCells + int(buckets[pixel])
					if candidate := db.bucketSampler.draw(bucket, next()); candidate >= 0 {
						return candidate
					}
				}
				return db.parentSampler.draw(int(parent), next())
			}
			// shifted returns the database position displaced by (dy, dx) from
			// a match, or -1 when it leaves the match's pyramid phase.
			shifted := func(match int32, dy, dx int) int32 {
				if match < 0 {
					return -1
				}
				y, x := int(match)/db.width, int(match)%db.width
				phaseBase := y - y%db.phaseHeight
				y += dy
				x += dx
				if y < phaseBase || y >= phaseBase+db.phaseHeight || x < 0 || x >= db.width || y*db.width+x >= len(records) {
					return -1
				}
				return int32(y*db.width + x)
			}
			// neighbourError sums the tone error of the up-to-eight chosen
			// neighbours inside the tile and scales it by spread/8 per neighbour.
			neighbourError := func(pixel int) [3]int32 {
				var total [3]int32
				if spread == 0 {
					return total
				}
				y, x := pixel/sourceTileSize, pixel%sourceTileSize
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						ny, nx := y+dy, x+dx
						if (dy == 0 && dx == 0) || ny < 0 || ny >= sourceTileSize || nx < 0 || nx >= sourceTileSize {
							continue
						}
						err := &chosen[ny*sourceTileSize+nx]
						total[0] += err[0]
						total[1] += err[1]
						total[2] += err[2]
					}
				}
				for channel := range 3 {
					total[channel] = total[channel] * spread / 64
				}
				return total
			}
			// seamCost charges a candidate block for every edge pixel pair
			// with an already-chosen neighbour block whose squared RGB
			// distance exceeds the dead zone: the output should not be
			// speckled beyond what the authored map's adjacent pixels are.
			pair := func(a, b byte) int32 {
				d := pairDistance[int(a)*paletteSize+int(b)] - seamZone
				if d < 0 {
					return 0
				}
				return d
			}
			seamCost := func(pixel int, block *[4]byte) int32 {
				if seamWeight == 0 {
					return 0
				}
				y, x := pixel/sourceTileSize, pixel%sourceTileSize
				var total int32
				if x > 0 && tileMatches[pixel-1] >= 0 {
					left := &chosenBlock[pixel-1]
					total += pair(block[0], left[1]) + pair(block[2], left[3])
				}
				if x < sourceTileSize-1 && tileMatches[pixel+1] >= 0 {
					right := &chosenBlock[pixel+1]
					total += pair(block[1], right[0]) + pair(block[3], right[2])
				}
				if y > 0 && tileMatches[pixel-sourceTileSize] >= 0 {
					top := &chosenBlock[pixel-sourceTileSize]
					total += pair(block[0], top[2]) + pair(block[1], top[3])
				}
				if y < sourceTileSize-1 && tileMatches[pixel+sourceTileSize] >= 0 {
					bottom := &chosenBlock[pixel+sourceTileSize]
					total += pair(block[2], bottom[0]) + pair(block[3], bottom[1])
				}
				return seamWeight * total / 8
			}
			direction := 0
			// coherenceCost compares the authored pixels around the candidate
			// block (in the atlas, at full resolution) with the output pixels
			// already synthesized around the query block. Only atlas examples
			// have an authored surround; supplements are exempt. Valid atlas
			// positions keep a four-pixel margin inside their cell, so the
			// neighbourhood never leaves the atlas. The term rewards copying
			// contiguous authored structure.
			coherenceCost := func(pixel int, candidate int32) int32 {
				if coherenceWeight == 0 || int(candidate) >= atlasPositions {
					return 0
				}
				y, x := int(candidate)/db.width, int(candidate)%db.width
				phase := y / db.phaseHeight
				sourceBase := (phase/2+2*(y%db.phaseHeight))*atlas.width + phase%2 + 2*x
				outBase := (2*(pixel/sourceTileSize)+2)*synthWidth + 2*(pixel%sourceTileSize) + 2
				synthDelta, atlasDelta := &synthDeltas[direction], &atlasDeltas[direction]
				var total int32
				for index := range 16 {
					out := synthesized[outBase+synthDelta[index]]
					if out == 0xff {
						continue
					}
					total += pairDistance[int(atlas.pix[sourceBase+atlasDelta[index]])*paletteSize+int(out)]
				}
				return coherenceWeight * total / 16
			}
			toneCost := func(err [3]int32, offset [3]int32) int32 {
				d0 := softenAbs(err[0]+offset[0], deadzone)
				d1 := softenAbs(err[1]+offset[1], deadzone)
				d2 := softenAbs(err[2]+offset[2], deadzone)
				return toneWeight * (d0*d0 + d1*d1 + d2*d2)
			}
			// visit evaluates the propagation candidates already in
			// candidates[:count], then (unless the pixel is settled) the
			// random-window and global draws, against the pixel's current
			// match, and commits the best. Neighbour state does not change
			// during a visit, so the neighbour tone error and the current
			// match's full cost are evaluated once.
			visit := func(pixel int, count int, draws bool) {
				parent := parents[pixel]
				feat := &features[pixel]
				target := &targets[parent]
				offset := neighbourError(pixel)
				bestFeat, best := featCosts[pixel], tileMatches[pixel]
				bestErr := chosen[pixel]
				bestBlock := chosenBlock[pixel]
				bestCost := bestFeat
				if bestFeat != infCost {
					if toneWeight > 0 {
						bestCost += toneCost(bestErr, offset)
					}
					bestCost += seamCost(pixel, &bestBlock) + coherenceCost(pixel, best)
				}
				try := func(candidate int32) {
					if candidate < 0 {
						return
					}
					entry := &records[candidate]
					if entry.key < 0 || !db.allowed[parent][entry.key] {
						return
					}
					feat := featureCost(feat, &entry.feat)
					cost := feat
					if cost >= bestCost {
						return
					}
					sum := blockSum(&entry.block, &sums)
					err := [3]int32{sum[0] - target[0], sum[1] - target[1], sum[2] - target[2]}
					if toneWeight > 0 {
						cost += toneCost(err, offset)
					}
					cost += seamCost(pixel, &entry.block) + coherenceCost(pixel, candidate)
					if cost < bestCost {
						bestCost, bestFeat, best, bestErr, bestBlock = cost, feat, candidate, err, entry.block
					}
				}
				for _, candidate := range candidates[:count] {
					try(candidate)
				}
				if draws {
					for _, radius := range [...]int{8, 4, 2, 1} {
						random := next()
						span := uint64(2*radius + 1)
						dy := int(random%span) - radius
						dx := int((random>>32)%span) - radius
						try(shifted(best, dy, dx))
					}
					try(globalDraw(pixel, true))
					try(globalDraw(pixel, false))
				}
				if best == tileMatches[pixel] {
					if unchanged[pixel] < 255 {
						unchanged[pixel]++
					}
					return
				}
				unchanged[pixel] = 0
				featCosts[pixel], tileMatches[pixel], chosen[pixel], chosenBlock[pixel] = bestFeat, best, bestErr, bestBlock
				if coherenceWeight > 0 && best >= 0 {
					outY, outX := 2*(pixel/sourceTileSize)+2, 2*(pixel%sourceTileSize)+2
					synthesized[outY*synthWidth+outX] = bestBlock[0]
					synthesized[outY*synthWidth+outX+1] = bestBlock[1]
					synthesized[(outY+1)*synthWidth+outX] = bestBlock[2]
					synthesized[(outY+1)*synthWidth+outX+1] = bestBlock[3]
				}
			}

			for pixel := range tilePixels {
				buckets[pixel] = int32(hashBin(&features[pixel]))
			}
			for index := range synthesized {
				synthesized[index] = 0xff
			}
			clear(unchanged[:])
			for pixel := range tilePixels {
				featCosts[pixel], tileMatches[pixel], chosen[pixel], chosenBlock[pixel] = infCost, -1, [3]int32{}, [4]byte{}
				for slot := range 4 {
					candidates[slot] = globalDraw(pixel, slot%2 == 0)
				}
				visit(pixel, 4, false)
			}
			for iteration := range iterations {
				forward := iteration%2 == 0
				direction = iteration % 2
				for scan := range tilePixels {
					pixel := scan
					step := 1
					if !forward {
						pixel = tilePixels - 1 - scan
						step = -1
					}
					y, x := pixel/sourceTileSize, pixel%sourceTileSize
					count := 0
					if forward && x > 0 || !forward && x < sourceTileSize-1 {
						candidates[count] = shifted(tileMatches[pixel-step], 0, step)
						count++
					}
					if forward && y > 0 || !forward && y < sourceTileSize-1 {
						candidates[count] = shifted(tileMatches[pixel-step*sourceTileSize], step, 0)
						count++
					}
					draws := settle == 0 || int(unchanged[pixel]) < settle
					visit(pixel, count, draws)
				}
			}
			copy(costs[base:base+tilePixels], featCosts[:])
		}
	})
	mean, unmatched := costSummary(costs)
	fmt.Printf("iterations=%d mean-cost=%.2f unmatched=%d\n", iterations, mean, unmatched)
	return matches, costs, unmatched
}

// relaxParents lets a query with parent p also use blocks whose reduction is
// a palette entry k one ALP step away: blending p with k snaps to p or k, so no
// palette entry lies between them. The retail blend rounds to the nearest
// entry, so on a sparse palette the authored blocks under p can all be
// brighter than p [03 §3.7]; the tone term could then only reach p's colour
// with uniform blocks, which show as a 2x2 grid, while the adjacent entries'
// blocks carry the same texture at the right mean. The neighbourhood comes
// from the map's own ALP table, so it is tight on dense palettes and wide on
// sparse ones. A stand-in is only admitted when its blocks are on average
// closer to the parent's colour than the parent's own blocks, so relaxation
// can only reduce the bias, never add examples that are further off tone.
// Random draws pick a stand-in parent with probability proportional to its
// number of examples, as if the lists were merged.
func relaxParents(db *database, records []record, alp, palette []byte, relax bool) {
	var entries []int32
	var offsets, counts [paletteSize]int32
	var weights [paletteSize]uint32
	var meanSum [paletteSize][3]float64
	sums := paletteSums(palette)
	for key := range paletteSize {
		weights[key] = uint32(db.counts[key])
		for _, position := range db.positions[db.offsets[key] : db.offsets[key]+db.counts[key]] {
			sum := blockSum(&records[position].block, &sums)
			for channel := range 3 {
				meanSum[key][channel] += float64(sum[channel])
			}
		}
		for channel := range 3 {
			meanSum[key][channel] /= float64(max(db.counts[key], 1))
		}
	}
	toneDistance := func(parent, key int) float64 {
		total := 0.0
		for channel := range 3 {
			d := meanSum[key][channel] - 4*float64(palette[3*parent+channel])
			total += d * d
		}
		return total
	}
	for parent := range paletteSize {
		offsets[parent] = int32(len(entries))
		for key := range paletteSize {
			if db.counts[key] == 0 {
				continue
			}
			blend := int(alp[parent*paletteSize+key])
			if key != parent && (!relax || blend != parent && blend != key ||
				toneDistance(parent, key) >= toneDistance(parent, parent)) {
				continue
			}
			db.allowed[parent][key] = true
			entries = append(entries, int32(key))
			counts[parent]++
		}
	}
	db.neighbours = makeSampler(entries, offsets[:], counts[:], weights[:])
	for parent := range paletteSize {
		db.sole[parent] = -1
		if counts[parent] == 1 {
			db.sole[parent] = int16(entries[offsets[parent]])
		}
	}
}

// cycleConsistency is the fraction of matched pixels whose block reduces to
// exactly the query's parent, 1 when no relaxation was used.
func cycleConsistency(parents []byte, matches []int32, records []record) float64 {
	exact, total := 0, 0
	for pixel, match := range matches {
		if match < 0 {
			continue
		}
		total++
		if records[match].key == int16(parents[pixel]) {
			exact++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(exact) / float64(total)
}

// softenAbs returns |value| reduced by the dead zone, clamped at zero.
func softenAbs(value, deadzone int32) int32 {
	if value < 0 {
		value = -value
	}
	if value <= deadzone {
		return 0
	}
	return value - deadzone
}

// featureCost is the squared PCA distance in native units, undoing the
// record's per-dimension quantization scales.
func featureCost(a, b *[featureDims]int8) int32 {
	d0 := int32(a[0]) - int32(b[0])
	d1 := int32(a[1]) - int32(b[1])
	d2 := int32(a[2]) - int32(b[2])
	d3 := int32(a[3]) - int32(b[3])
	d4 := int32(a[4]) - int32(b[4])
	d5 := int32(a[5]) - int32(b[5])
	d6 := int32(a[6]) - int32(b[6])
	d7 := int32(a[7]) - int32(b[7])
	return featureScaleFirst*featureScaleFirst*d0*d0 +
		featureScaleRest*featureScaleRest*(d1*d1+d2*d2+d3*d3+d4*d4+d5*d5+d6*d6+d7*d7)
}

func costSummary(costs []int32) (float64, int) {
	total := 0.0
	count, unmatched := 0, 0
	for _, cost := range costs {
		if cost == infCost {
			unmatched++
			continue
		}
		total += float64(cost)
		count++
	}
	return total / float64(max(count, 1)), unmatched
}
