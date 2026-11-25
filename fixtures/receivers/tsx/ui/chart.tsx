import { TsxLegend as RemoteLegend, TsxChart as RemoteChart } from "some-chart-library";

// A component that wraps a library component of the same name. The renamed
// import is the library's, so neither use is this file's own TsxChart or
// TsxLegend.
export function TsxChart() { // def tsx.chart.TsxChart
  return <RemoteChart>text</RemoteChart>; // call RemoteChart=TsxChart -> external
}

export function TsxLegend() { // def tsx.chart.TsxLegend
  return <RemoteLegend />; // call RemoteLegend=TsxLegend -> external
}
