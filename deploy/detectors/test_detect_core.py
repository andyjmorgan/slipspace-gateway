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


class PiiCategory(unittest.TestCase):
    def test_known_presidio_labels(self):
        self.assertEqual(dc.pii_category("EMAIL_ADDRESS"), "pii.email")
        self.assertEqual(dc.pii_category("US_SSN"), "pii.ssn")
        self.assertEqual(dc.pii_category("PERSON"), "pii.person")

    def test_known_openai_labels(self):
        self.assertEqual(dc.pii_category("EMAIL"), "pii.email")
        self.assertEqual(dc.pii_category("CREDITCARDNUMBER"), "pii.credit_card")
        self.assertEqual(dc.pii_category("FIRSTNAME"), "pii.person")

    def test_case_and_separator_insensitive(self):
        self.assertEqual(dc.pii_category("email_address"), "pii.email")
        self.assertEqual(dc.pii_category("phone-number"), "pii.phone")
        self.assertEqual(dc.pii_category(" Person "), "pii.person")

    def test_openai_private_prefix_unifies_with_presidio(self):
        # openai/privacy-filter labels carry a private_ prefix; both engines
        # must converge on the same canonical category.
        self.assertEqual(dc.pii_category("private_email"), dc.pii_category("EMAIL_ADDRESS"))
        self.assertEqual(dc.pii_category("private_person"), "pii.person")
        self.assertEqual(dc.pii_category("private_phone"), "pii.phone")
        self.assertEqual(dc.pii_category("private_address"), "pii.address")
        self.assertEqual(dc.pii_category("private_url"), "pii.url")
        self.assertEqual(dc.pii_category("account_number"), "pii.bank_account")
        self.assertEqual(dc.pii_category("secret"), "pii.secret")

    def test_unknown_private_prefix_stripped_in_fallback(self):
        # a future private_<x> with no mapping slugs the bare entity, not private_x.
        self.assertEqual(dc.pii_category("private_passport"), "pii.passport")
        self.assertEqual(dc.pii_category("private_widget"), "pii.widget")

    def test_unknown_label_falls_back_not_dropped(self):
        # a new entity type still surfaces as a finding, slugged.
        self.assertEqual(dc.pii_category("VEHICLE_VIN"), "pii.vehicle_vin")
        self.assertEqual(dc.pii_category(""), "pii.unknown")


class MergeSpans(unittest.TestCase):
    def test_below_threshold_dropped(self):
        fs = [dc.Finding(category="pii.email", score=0.3, start=0, end=5)]
        self.assertEqual(dc.merge_spans(fs, 0.5), [])

    def test_overlapping_same_category_collapses_to_max(self):
        # two engines flag the same email at slightly different spans.
        fs = [
            dc.Finding(category="pii.email", score=0.8, raw_label="openai:EMAIL", start=10, end=25),
            dc.Finding(category="pii.email", score=0.95, raw_label="presidio:EMAIL_ADDRESS", start=10, end=27),
        ]
        out = dc.merge_spans(fs, 0.5)
        self.assertEqual(len(out), 1)
        self.assertAlmostEqual(out[0].score, 0.95)
        self.assertEqual(out[0].start, 10)
        self.assertEqual(out[0].end, 27)  # widened to the union
        self.assertEqual(out[0].raw_label, "presidio:EMAIL_ADDRESS")  # higher score's label

    def test_overlapping_different_category_both_kept(self):
        fs = [
            dc.Finding(category="pii.person", score=0.9, start=0, end=10),
            dc.Finding(category="pii.location", score=0.9, start=5, end=15),
        ]
        out = dc.merge_spans(fs, 0.5)
        self.assertEqual(len(out), 2)

    def test_disjoint_same_category_both_kept(self):
        fs = [
            dc.Finding(category="pii.email", score=0.9, start=0, end=5),
            dc.Finding(category="pii.email", score=0.9, start=20, end=30),
        ]
        out = dc.merge_spans(fs, 0.5)
        self.assertEqual(len(out), 2)

    def test_whole_unit_findings_deduped_per_category(self):
        fs = [
            dc.Finding(category="pii.email", score=0.7),
            dc.Finding(category="pii.email", score=0.9),
        ]
        out = dc.merge_spans(fs, 0.5)
        self.assertEqual(len(out), 1)
        self.assertAlmostEqual(out[0].score, 0.9)


class ToByteSpans(unittest.TestCase):
    def test_ascii_offsets_unchanged(self):
        text = "email me at a@b.com"
        fs = [dc.Finding(category="pii.email", score=0.9, start=12, end=19)]
        out = dc.to_byte_spans(fs, text)
        self.assertEqual((out[0].start, out[0].end), (12, 19))
        self.assertEqual(text[12:19], "a@b.com")

    def test_multibyte_chars_shift_offsets(self):
        # "café " is 5 chars but 6 bytes (é = 2 bytes); span after it shifts.
        text = "café x@y.com"
        cstart, cend = text.index("x@y.com"), text.index("x@y.com") + 7
        fs = [dc.Finding(category="pii.email", score=0.9, start=cstart, end=cend)]
        out = dc.to_byte_spans(fs, text)
        self.assertEqual(out[0].start, len(text[:cstart].encode("utf-8")))
        self.assertEqual(text.encode("utf-8")[out[0].start:out[0].end].decode("utf-8"), "x@y.com")

    def test_whole_unit_passes_through(self):
        fs = [dc.Finding(category="pii.email", score=0.9)]
        out = dc.to_byte_spans(fs, "text")
        self.assertIsNone(out[0].start)


if __name__ == "__main__":
    unittest.main()
