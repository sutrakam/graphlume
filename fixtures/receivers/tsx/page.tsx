import { TsxButton } from "./ui/button";
import { TsxButton as TsxPrimaryButton } from "./ui/button";
import { TsxCard, TsxPanel, TsxBox, TsxLegacy } from "./ui/wrapped";
import * as Parts from "./ui/parts";
import { TsxList } from "./ui/parts";
import { Spinner } from "some-library";

export function TsxPage() { // def tsx.page.TsxPage
  const a = <TsxButton label="x" />; // call TsxButton -> tsx.button.TsxButton
  const b = <TsxCard />; // call TsxCard -> tsx.wrapped.TsxCard
  const c = <TsxPanel>text</TsxPanel>; // call TsxPanel -> tsx.wrapped.TsxPanel
  const d = <TsxBox></TsxBox>; // call TsxBox -> tsx.wrapped.TsxBox
  const e = <TsxLegacy />; // call TsxLegacy -> new tsx.wrapped.TsxLegacy
  const f = <Parts.TsxItem />; // call TsxItem -> tsx.parts.TsxItem
  const g = <Spinner />; // call Spinner -> external
  const h = <main>text</main>; // nocall main
  const i = <div><TsxButton label="y" /></div>; // call TsxButton -> tsx.button.TsxButton; nocall div
  const j = <TsxList.Item />; // call Item -> tsx.parts.TsxListItem
  const k = <TsxPrimaryButton label="z" />; // call TsxPrimaryButton=TsxButton -> tsx.button.TsxButton
  return [a, b, c, d, e, f, g, h, i, j, k];
}
