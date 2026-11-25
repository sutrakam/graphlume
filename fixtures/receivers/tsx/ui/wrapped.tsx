import { forwardRef, memo } from "react";
import styled from "styled-components";

export const TsxCard = forwardRef<HTMLDivElement, {}>((props, ref) => <div ref={ref} />); // def tsx.wrapped.TsxCard

export const TsxPanel = memo(function TsxPanelImpl() { // def tsx.wrapped.TsxPanel
  return <i />;
});

export const TsxBox = styled.div`color: red;`; // def tsx.wrapped.TsxBox

export class TsxLegacy extends React.Component { // def tsx.wrapped.TsxLegacy
  render() { // def tsx.wrapped.render
    return null;
  }
}
