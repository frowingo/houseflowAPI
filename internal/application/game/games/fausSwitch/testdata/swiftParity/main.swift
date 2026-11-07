// Swift side of TestSwiftParityTrace (swiftParity_test.go). It is not built
// with the Go module. To regenerate digests.txt, copy FausSwitchCourse.swift,
// FausSwitchModels.swift, FausSwitchPhysics.swift, FausSwitchTrack.swift and the
// FausSwitchViewport struct from FausSwitchRenderModels.swift (FausCrewCore,
// courseVersion 7; never the bot files) next to this file outside both repos,
// then run `swiftc -O -o trace *.swift && ./trace > swift.txt` and hash each
// "scenario" block (header line included) with sha256, one
// "<header>\t<hex>" row per scenario.
import Foundation

// Seeded flip races for the Go port's parity check. Every tick: each seat in
// order draws from a shared LCG; a draw of 0 mod `every` flips that seat; then
// the whole field steps. One line per runner per tick.
struct LCG { var state: UInt64
    mutating func next() -> UInt64 { state = state &* 6364136223846793005 &+ 1442695040888963407; return state >> 33 } }

func bits(_ v: Double) -> String { String(v.bitPattern, radix: 16) }
func ids(_ s: Set<Int>) -> String { s.sorted().map(String.init).joined(separator: ",") }

var out = ""
for count in 1...8 {
    for seed in UInt64(1)...3 {
        for every in [UInt64(25), 60] {
            let course = FausSwitchCourse.race(runnerCount: count)
            var runners = FausSwitchStartGrid.lanes(count: count, runnerScale: FausSwitchStartGrid.runnerScale(runnerCount: count)).map(\.spawn)
            var random = LCG(state: seed &* 1000 &+ UInt64(count))
            out += "scenario \(count) \(seed) \(every)\n"
            var tick: Int64 = 0
            while tick < 14_400 && runners.contains(where: \.isRunning) {
                for seat in runners.indices where random.next() % every == 0 {
                    let outcome = FausSwitchPhysics.flip(&runners, index: seat, course: course)
                    out += "flip \(tick) \(seat) \(outcome)\n"
                }
                FausSwitchPhysics.step(&runners, tick: tick, course: course)
                tick += 1
                for (seat, r) in runners.enumerated() {
                    out += "\(tick) \(seat) \(bits(r.x)) \(bits(r.y)) \(bits(r.velocityY)) \(r.gravity) \(r.status) \(r.finishedAtTick ?? -1) \(r.crashedAtTick ?? -1) \(r.crashReason.map { "\($0)" } ?? "none") \(r.boostedUntilTick) \(r.slowedUntilTick) [\(ids(r.usedBoosts))] [\(ids(r.usedSlows))]\n"
                }
            }
        }
    }
}
FileHandle.standardOutput.write(out.data(using: .utf8)!)
