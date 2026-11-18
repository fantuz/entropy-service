"""Validate discovered limits and performance without starting GTK."""
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tools/desktop-gui"))
from entropy_gui.model import ReportError, StreamModel, performance_text
from entropy_gui.options import fields_for
from test_desktop_gui import start_record, sample_record


def performance():
    return {"requested_fps": 20.0, "effective_target_fps": 10.0, "average_received_fps": 9.8,
            "requested_mib_per_second": 640.0, "effective_interval_ms": 100.0,
            "output_ms": 0.0, "total_analysis_ms": 10.0, "pacing_status": "adaptive paced"}


class PerformanceTests(unittest.TestCase):
    def test_discovered_limits_performance_and_old_reports(self):
        model = StreamModel()
        record = start_record()
        model.consume(record)
        self.assertIn("not reported", performance_text(model.snapshot()))
        record["discovery"] = {"status": "available", "document": {"schema_version": 1,
            "service": "entropy-service", "max_bytes": 65536, "private_extra": "do not retain"}}
        model.consume(record)
        model.consume({**sample_record(), **performance(), "receive_wait_ms": 80.0, "analysis_ms": 2.0})
        state = model.snapshot()
        self.assertEqual(state["discovery"], {"status": "available", "max_bytes": 65536})
        text = performance_text(state)
        for value in ("65,536", "20.00 requested", "10.00 effective", "9.80 average", "adaptive paced"):
            self.assertIn(value, text)
        state["discovery"]["max_bytes"] = 1
        self.assertEqual(model.snapshot()["discovery"]["max_bytes"], 65536)
        model.reset()
        self.assertFalse(model.snapshot()["discovery"])

    def test_invalid_capabilities_or_timings_stop_the_report(self):
        for maximum in (0, -1, True, 33554433):
            model = StreamModel()
            record = start_record()
            record["discovery"] = {"status": "available", "document": {"schema_version": 1,
                "service": "entropy-service", "max_bytes": maximum}}
            with self.assertRaises(ReportError):
                model.consume(record)
        for changed in ({"effective_interval_ms": -1}, {"average_received_fps": float("inf")},
                        {"output_ms": True}, {"pacing_status": "healthy RNG"}):
            with self.assertRaises(ReportError):
                StreamModel().consume({**sample_record(), **performance(), **changed})

    def test_editor_exposes_discovery_and_optional_adaptive_limits(self):
        names = {field.name for field in fields_for("client")}
        self.assertTrue({"capabilities", "adaptive", "adaptive-max-interval"} <= names)


if __name__ == "__main__":
    unittest.main()
