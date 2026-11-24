export async function handle(req: Request): Promise<string> { // def ts.handle
  effect(req); // call effect -> none
  return req.text(); // call text -> external
}
