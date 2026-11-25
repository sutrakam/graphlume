import * as React from "react";
import { forwardRef, memo, createContext } from "react";
import styled from "styled-components";
import { Button } from "./button";

export const ThemeContext = createContext<string>("light");
export const MAX_ITEMS = 5;
const hidden = 3;

export function Page(props: { title: string }) {
  return (
    <ThemeContext.Provider value="dark">
      <Button variant="primary">{props.title}</Button>
      <Fancy />
    </ThemeContext.Provider>
  );
}

export const Fancy = forwardRef<HTMLDivElement, { label?: string }>((props, ref) => (
  <div ref={ref}>{props.label}</div>
));

export const Memoised = memo(function Memoised(p: { n: number }) {
  return <span>{p.n}</span>;
});

const Lazy = React.lazy(() => import("./lazy"));
const Box = styled.div`
  color: red;
`;
const Wide = styled(Box)`
  width: 100%;
`;
export const Arrow = (): JSX.Element => <Box />;

export class Legacy extends React.Component<{ n: number }> {
  render() {
    return <Lazy />;
  }
}

export interface Props { n: number }
export type Size = "s" | "m";
