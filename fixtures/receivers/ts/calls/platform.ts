// The client's own Response type, declared in this directory.
export interface Response { // def ts.platform.Response
  clone(): Response; // def ts.platform.clone
}

export class TsRequestLog { // def ts.platform.TsRequestLog
  size(): number { return 0; } // def ts.platform.size
}
