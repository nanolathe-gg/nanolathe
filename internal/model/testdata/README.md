# Synthetic model fixtures [03 §2.4] [fmt 3do]

Fixtures in this directory are authored for tests and are NOT copied retail bytes.

* `buildHalfTurn3DO` in `model_test.go` synthesizes a minimal 3DO with one object
  at translation (2,3,4) and vertex (10,20,30) to verify the half-turn negation
  (negate X and Z) per [03 §2.4] C20.
* Synthetic hierarchies for rotation-order, translation summing and leaf-attachment
  tests are constructed directly as `Model` structs in `model_test.go` — they are
  not read from retail files and live only in test memory.
* No retail 3DO bytes are stored here.

All synthetic fixtures use 16.16 Fixed values per docs/INVARIANTS.md I2.
