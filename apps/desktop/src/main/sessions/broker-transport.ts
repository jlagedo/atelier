// Production transport for PartisanClient: the broker exec door. stdout/stderr stream
// in via execStream's callback; control messages go out via execInput, base64-encoded
// (the RPC requires it). Keeping the b64 encoding HERE — not in PartisanClient — is what
// lets the same client also run over a raw subprocess pipe.

import type { HostClient } from "../host-client";
import type { ExecParams, LoopControl } from "../host-client/types";
import type { LoopTransport, LoopTransportFactory } from "./transport";

const b64line = (obj: LoopControl) =>
  Buffer.from(JSON.stringify(obj) + "\n", "utf8").toString("base64");

export function brokerTransport(host: HostClient, exec: ExecParams): LoopTransportFactory {
  if (!exec.sessionId) throw new Error("brokerTransport requires exec.sessionId for stdin routing");
  const { id, sessionId } = exec;
  return (onOutput): LoopTransport => {
    const run = host.execStream(exec, onOutput);
    return {
      send: (control) => host.execInput({ id, sessionId, data: b64line(control) }),
      close: () => run.close(),
      done: run.result,
    };
  };
}
