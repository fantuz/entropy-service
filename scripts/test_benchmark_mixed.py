import json
from pathlib import Path
from tempfile import TemporaryDirectory
import unittest

from benchmark_mixed import MIB, summarize


def sample(frame, wait):
    return {'type': 'sample', 'frame_bytes': MIB, 'received_frames': frame, 'receive_wait_ms': wait}


def summary(frames, **fields):
    return {'type': 'run_summary', 'termination': 'duration', 'failed': False, 'measurement_began': True,
            'received_frames': frames, 'received_bytes': frames * MIB, **fields}


class MixedReportsTest(unittest.TestCase):
    def summarize(self, *clients):
        with TemporaryDirectory() as directory:
            paths = []
            for index, records in enumerate(clients):
                path = Path(directory) / f'client-{index}.jsonl'
                path.write_text(''.join(json.dumps(record) + '\n' for record in records))
                paths.append(path)
            return summarize(paths, 2, MIB)

    def test_aggregate_delivered_bytes_and_sampled_waits(self):
        # Frame numbers may skip when a received frame has no separate timing record.
        result = self.summarize([sample(1, 0), sample(4, 3), summary(5)],
                                [sample(1, 12), summary(1)])
        self.assertEqual(result, {'frames_timed': 3, 'received_mib_per_s': 3,
                                  'receive_p95_ms': 12, 'receive_p99_ms': 12})

    def test_completed_worker_cannot_mask_an_incomplete_peer(self):
        for records in ([summary(0)], [sample(1, 2)], [sample(2, 1), summary(1)],
                        [sample(1, 2), summary(1, received_bytes=0)],
                        [sample(1, 2), summary(1, failed=True)],
                        [sample(1, 2), summary(1, termination='interrupted')],
                        [sample(1, 2), summary(1), sample(2, 3)]):
            with self.subTest(records=records), self.assertRaises(ValueError):
                self.summarize([sample(1, 2), summary(1)], records)

    def test_bad_frames_cannot_contribute_latency(self):
        for record in (sample(1, -1), sample(1, float('nan')),
                       sample(1, float('inf')), dict(sample(1, 1), frame_bytes=256)):
            with self.subTest(record=record), self.assertRaises(ValueError):
                self.summarize([record, summary(1)])


if __name__ == '__main__':
    unittest.main()
