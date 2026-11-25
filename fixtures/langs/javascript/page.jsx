import { Card } from './card';

export function Page() {
  return <Card title="x"><span>text</span></Card>;
}

export const Shell = () => <Page />;
