// Package load is the load generator and its measurement (WP-L8, spec
// 05 §7): a tier of simulated operators (telemetry/v1 at 1 Hz over the
// USSP's WS /v1/telemetry, through internal/simop), Remote ID receivers
// (rid/observation/v1 to the authority, through internal/simrx, BT5 packs
// and BT4 single messages), the consoles that read the picture and the
// traffic streams, synthetic paths over the demo area with scripted
// conflicts whose alerts are known in advance, and the report: every row
// of 05 §7 as checks with the observed figure, pass, fail or "not
// measured" with the reason (never a blank, LESSONS E-04).
//
// Every latency is end to end on one clock, the generator's: from the
// moment a sample is handed to the client that sends it to the moment
// the frame that shows it arrives on a console. The target's clocks are
// never subtracted from ours (a skewed system clock cannot make a number
// look better). A frame is tied to the sample it shows by the aircraft
// and its captured_at (the nearest sample sent within MatchWindow), so a
// frame that shows nothing the generator sent is counted, not timed.
//
// A run that measures nothing fails: the verdict is pass only when every
// check the tier requires was measured on at least its minimum number of
// samples and passed, no measured check failed, and at least one was
// measured at all.
package load
