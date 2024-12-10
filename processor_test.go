package plotthread

import "testing"

func TestComputeMaxRepresentationsPerPlot(t *testing.T) {
	var maxDoublings int64 = 64
	var doublings int64
	previous := INITIAL_MAX_REPRESENTATIONS_PER_PLOT / 2
	// verify the max is always doubling as expected
	for doublings = 0; doublings < maxDoublings; doublings++ {
		var height int64 = doublings * PLOTS_UNTIL_REPRESENTATIONS_PER_PLOT_DOUBLING
		max := computeMaxRepresentationsPerPlot(height)
		if max < INITIAL_MAX_REPRESENTATIONS_PER_PLOT {
			t.Fatalf("Max %d at height %d less than initial", max, height)
		}
		expect := previous * 2
		if expect > MAX_REPRESENTATIONS_PER_PLOT {
			expect = MAX_REPRESENTATIONS_PER_PLOT
		}
		if max != expect {
			t.Fatalf("Max %d at height %d not equal to expected max %d",
				max, height, expect)
		}
		if doublings > 0 {
			var previous2 int = max
			// walk back over the previous period and make sure:
			// 1) the max is never greater than this period's first max
			// 2) the max is always <= the previous as we walk back
			for height -= 1; height >= (doublings-1)*PLOTS_UNTIL_REPRESENTATIONS_PER_PLOT_DOUBLING; height-- {
				max2 := computeMaxRepresentationsPerPlot(height)
				if max2 > max {
					t.Fatalf("Max %d at height %d is greater than next period's first max %d",
						max2, height, max)
				}
				if max2 > previous2 {
					t.Fatalf("Max %d at height %d is greater than previous max %d at height %d",
						max2, height, previous2, height+1)
				}
				previous2 = max2
			}
		}
		previous = max
	}
	max := computeMaxRepresentationsPerPlot(MAX_REPRESENTATIONS_PER_PLOT_EXCEEDED_AT_HEIGHT)
	if max != MAX_REPRESENTATIONS_PER_PLOT {
		t.Fatalf("Expected %d at height %d, found %d",
			MAX_REPRESENTATIONS_PER_PLOT, MAX_REPRESENTATIONS_PER_PLOT_EXCEEDED_AT_HEIGHT, max)
	}
	max = computeMaxRepresentationsPerPlot(MAX_REPRESENTATIONS_PER_PLOT_EXCEEDED_AT_HEIGHT + 1)
	if max != MAX_REPRESENTATIONS_PER_PLOT {
		t.Fatalf("Expected %d at height %d, found",
			MAX_REPRESENTATIONS_PER_PLOT, MAX_REPRESENTATIONS_PER_PLOT_EXCEEDED_AT_HEIGHT+1)
	}
	max = computeMaxRepresentationsPerPlot(MAX_REPRESENTATIONS_PER_PLOT_EXCEEDED_AT_HEIGHT - 1)
	if max >= MAX_REPRESENTATIONS_PER_PLOT {
		t.Fatalf("Expected less than max at height %d, found %d",
			MAX_REPRESENTATIONS_PER_PLOT_EXCEEDED_AT_HEIGHT-1, max)
	}
}
