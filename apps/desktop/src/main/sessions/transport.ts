// The transport seam under PartisanClient: the bytes pipe to the in-guest agent's
// stdin/stdout, abstracted so the SAME client runs over the broker exec door (prod)
// or a spawned subprocess (tests). Keeping this thin is what lets the wire client be
// tested against the real agent without a VM.

import type { LoopControl } from "../host-client/types";

export type LoopOutputStream = "stdout" | "stderr";

export interface LoopTransport {
  /** Deliver a control message to the agent's stdin (the transport encodes it). */
  send(control: LoopControl): Promise<void> | void;
  /** Hard-abort the run (drop the connection / kill the process). */
  close(): void;
  /** Resolves with the agent's exit code when it ends; may reject on abort/error. */
  readonly done: Promise<{ exitCode: number }>;
}

/** Builds a transport, wiring its stdout/stderr to the given sink. */
export type LoopTransportFactory = (
  onOutput: (stream: LoopOutputStream, data: Buffer) => void,
) => LoopTransport;
