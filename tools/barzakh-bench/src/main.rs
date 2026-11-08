//! Isolated timing harness for the pinned upstream v1 implementation.
//! This is not a server backend or an assessment of cryptographic security.

use barzakh_521::{Barzakh521, Barzakh521Batch};
use std::{
    hint::black_box,
    time::{Duration, Instant},
};

// Same public 00..3f benchmark seed as BenchmarkDRBGLifecycle; never deploy it.
const SEED: [u8; 64] = {
    let mut seed = [0; 64];
    let mut i = 0;
    while i < seed.len() {
        seed[i] = i as u8;
        i += 1;
    }
    seed
};

fn measure(mut operation: impl FnMut(), minimum: Duration) -> (u64, Duration) {
    operation(); // Warm up outside the timed region.
    let started = Instant::now();
    let mut operations = 0u64;
    loop {
        operation();
        operations += 1;
        if started.elapsed() >= minimum {
            return (operations, started.elapsed());
        }
    }
}

fn report(name: &str, size: usize, repetition: u32, result: (u64, Duration)) {
    let (operations, elapsed) = result;
    let seconds = elapsed.as_secs_f64();
    println!(
        "{{\"implementation\":\"{name}\",\"bytes_per_operation\":{size},\"repetition\":{repetition},\"operations\":{operations},\"seconds\":{seconds:.9},\"ns_per_operation\":{:.1},\"mb_per_second\":{:.6}}}",
        seconds * 1e9 / operations as f64,
        size as f64 * operations as f64 / seconds / 1e6,
    );
}

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let milliseconds: u64 = args
        .first()
        .map(|s| s.parse().expect("milliseconds must be an integer"))
        .unwrap_or(500);
    let repetitions: u32 = args
        .get(1)
        .map(|s| s.parse().expect("repetitions must be an integer"))
        .unwrap_or(5);
    assert!(
        args.len() <= 2,
        "usage: entropy-service-barzakh-bench [milliseconds=500] [repetitions=5]"
    );
    assert!(
        (100..=10_000).contains(&milliseconds),
        "milliseconds must be 100..10000"
    );
    assert!((1..=10).contains(&repetitions), "repetitions must be 1..10");
    let minimum = Duration::from_millis(milliseconds);

    for repetition in 1..=repetitions {
        report(
            "v1-single-new-drop",
            0,
            repetition,
            measure(
                || {
                    drop(black_box(Barzakh521::new(black_box(&SEED))));
                },
                minimum,
            ),
        );
        report(
            "v1-batch8-new-drop",
            0,
            repetition,
            measure(
                || {
                    drop(black_box(Barzakh521Batch::new(black_box(&SEED))));
                },
                minimum,
            ),
        );

        for size in [65_536, 1_048_576] {
            let mut buffer = vec![0u8; size];
            let mut single = Barzakh521::new(&SEED);
            report(
                "v1-single-fill",
                size,
                repetition,
                measure(
                    || {
                        for chunk in buffer.chunks_exact_mut(32) {
                            chunk.copy_from_slice(&single.next_u256());
                        }
                        black_box(&buffer);
                    },
                    minimum,
                ),
            );

            let mut batch = Barzakh521Batch::new(&SEED);
            report(
                "v1-batch8-fill",
                size,
                repetition,
                measure(
                    || {
                        batch.fill_bytes(&mut buffer);
                        black_box(&buffer);
                    },
                    minimum,
                ),
            );
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn upstream_aligned_reads_are_continuous() {
        let mut whole = Barzakh521Batch::new(&SEED);
        let mut split = Barzakh521Batch::new(&SEED);
        let mut expected = [0u8; 512];
        let mut actual = [0u8; 512];
        whole.fill_bytes(&mut expected);
        split.fill_bytes(&mut actual[..256]);
        split.fill_bytes(&mut actual[256..]);
        assert_eq!(expected, actual);
    }

    #[test]
    fn upstream_partial_reads_discard_batch_remainder() {
        let mut whole = Barzakh521Batch::new(&SEED);
        let mut split = Barzakh521Batch::new(&SEED);
        let mut expected = [0u8; 256];
        let mut actual = [0u8; 256];
        whole.fill_bytes(&mut expected);
        split.fill_bytes(&mut actual[..1]);
        split.fill_bytes(&mut actual[1..]);
        assert_eq!(expected[0], actual[0]);
        assert_ne!(
            expected, actual,
            "An io.Reader adapter must retain unconsumed bytes"
        );
    }
}
