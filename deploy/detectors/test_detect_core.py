"""Stdlib-only tests for detect_core (run: python3 -m unittest in this dir)."""

import unittest

import detect_core as dc


class ChunkWindows(unittest.TestCase):
    def test_fits_in_one_window(self):
        self.assertEqual(dc.chunk_windows(3, 4, 1), [(0, 3)])
        self.assertEqual(dc.chunk_windows(4, 4, 1), [(0, 4)])

    def test_overlap_covers_with_stride(self):
        # window 4, overlap 1 => stride 3; whole range covered, chunks overlap.
        self.assertEqual(dc.chunk_windows(10, 4, 1), [(0, 4), (3, 7), (6, 10)])

    def test_last_window_short(self):
        self.assertEqual(dc.chunk_windows(9, 4, 1), [(0, 4), (3, 7), (6, 9)])

    def test_empty(self):
        self.assertEqual(dc.chunk_windows(0, 4, 1), [])

    def test_overlap_clamped_to_progress(self):
        # overlap >= window would stall; clamped to 0 => contiguous windows.
        self.assertEqual(dc.chunk_windows(8, 4, 4), [(0, 4), (4, 8)])

    def test_no_gaps_between_chunks(self):
        wins = dc.chunk_windows(100, 16, 4)
        self.assertEqual(wins[0][0], 0)
        self.assertEqual(wins[-1][1], 100)
        for (a_s, a_e), (b_s, b_e) in zip(wins, wins[1:]):
            self.assertLessEqual(b_s, a_e, "a gap would leave content unscanned")


class ReduceFindings(unittest.TestCase):
    label_map = {"INJECTION": "injection.prompt_injection"}

    def test_positive_above_threshold(self):
        per_chunk = [[("SAFE", 0.1), ("INJECTION", 0.9)]]
        fs = dc.reduce_findings(per_chunk, 0.5, self.label_map)
        self.assertEqual(len(fs), 1)
        self.assertEqual(fs[0].category, "injection.prompt_injection")
        self.assertAlmostEqual(fs[0].score, 0.9)

    def test_below_threshold_dropped(self):
        per_chunk = [[("INJECTION", 0.3)]]
        self.assertEqual(dc.reduce_findings(per_chunk, 0.5, self.label_map), [])

    def test_benign_label_ignored(self):
        per_chunk = [[("SAFE", 0.99)]]
        self.assertEqual(dc.reduce_findings(per_chunk, 0.5, self.label_map), [])

    def test_overlap_dedup_keeps_max(self):
        # same category in two overlapping chunks => one finding, the higher score.
        per_chunk = [[("INJECTION", 0.7)], [("INJECTION", 0.95)]]
        fs = dc.reduce_findings(per_chunk, 0.5, self.label_map)
        self.assertEqual(len(fs), 1)
        self.assertAlmostEqual(fs[0].score, 0.95)

    def test_multilabel_toxicity(self):
        lm = {"toxic": "toxicity.toxic", "insult": "toxicity.insult"}
        per_chunk = [[("toxic", 0.8), ("insult", 0.6), ("threat", 0.9)]]
        fs = dc.reduce_findings(per_chunk, 0.5, lm)
        cats = sorted(f.category for f in fs)
        self.assertEqual(cats, ["toxicity.insult", "toxicity.toxic"])  # "threat" not mapped


class NormalizeScores(unittest.TestCase):
    def test_flat_list_of_dicts(self):
        raw = [{"label": "INJECTION", "score": 0.9}, {"label": "SAFE", "score": 0.1}]
        self.assertEqual(dc.normalize_scores(raw), [("INJECTION", 0.9), ("SAFE", 0.1)])

    def test_nested_single_wrapper(self):
        # some transformers versions wrap the per-input result in an extra list
        raw = [[{"label": "toxic", "score": 0.8}, {"label": "insult", "score": 0.2}]]
        self.assertEqual(dc.normalize_scores(raw), [("toxic", 0.8), ("insult", 0.2)])

    def test_empty(self):
        self.assertEqual(dc.normalize_scores([]), [])
        self.assertEqual(dc.normalize_scores(None), [])


class Contract(unittest.TestCase):
    def test_parse_camel_and_snake(self):
        camel = dc.parse_request({
            "correlationId": "c1",
            "unit": {"text": "hello"},
            "options": {"threshold": 0.5, "maxTokens": 512},
        })
        self.assertEqual((camel.correlation_id, camel.text, camel.threshold, camel.max_tokens),
                         ("c1", "hello", 0.5, 512))
        snake = dc.parse_request({
            "correlation_id": "c2",
            "unit": {"text": "hi"},
            "options": {"threshold": 0.1, "max_tokens": 256},
        })
        self.assertEqual((snake.correlation_id, snake.max_tokens), ("c2", 256))

    def test_parse_missing_fields(self):
        r = dc.parse_request({})
        self.assertEqual((r.correlation_id, r.text, r.threshold, r.max_tokens), ("", "", 0.0, 0))

    def test_build_response_shape(self):
        resp = dc.build_response(
            "c1",
            {"id": "inj", "model": "m", "version": "1", "family": "FAMILY_INJECTION"},
            [dc.Finding(category="injection.prompt_injection", score=0.9, raw_label="INJECTION")],
            dc.Scanned(tokens=600, truncated=False, chunks=2),
        )
        self.assertEqual(resp["schemaVersion"], "v1")
        self.assertEqual(resp["correlationId"], "c1")
        self.assertEqual(resp["status"], "STATUS_OK")
        self.assertEqual(resp["detector"]["family"], "FAMILY_INJECTION")
        self.assertEqual(resp["scanned"], {"tokens": 600, "truncated": False, "chunks": 2})
        self.assertEqual(len(resp["findings"]), 1)
        f = resp["findings"][0]
        self.assertEqual(f["category"], "injection.prompt_injection")
        self.assertEqual(f["rawLabel"], "INJECTION")
        self.assertEqual(f["localization"], "LOCALIZATION_NONE")
        self.assertNotIn("span", f)  # whole-unit finding has no span

    def test_build_response_with_span(self):
        resp = dc.build_response(
            "c", {"family": "FAMILY_PII"},
            [dc.Finding(category="pii.email", score=0.8, start=2, end=7)],
            dc.Scanned(),
        )
        f = resp["findings"][0]
        self.assertEqual(f["span"], {"start": 2, "end": 7, "basis": "OFFSET_BASIS_UTF8_BYTE"})
        self.assertEqual(f["localization"], "LOCALIZATION_EXACT")


if __name__ == "__main__":
    unittest.main()
