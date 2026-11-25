// A file in another directory that neither declares nor imports Response: the
// parameter is the platform's fetch Response, and its methods are not the
// repository's interface of that name.
export async function tsFetchCopy(res: Response): Promise<void> { // def ts.fetcher.tsFetchCopy
  res.clone(); // call clone -> external
  await res.text(); // call text -> external
}
