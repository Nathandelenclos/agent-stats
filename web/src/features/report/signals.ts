import { formatInteger } from "../../shared/format";

export interface PromptSignals {
  yelling: number;
  profanity: number;
  anguish: number;
  negation: number;
  blame: number;
  repeated: boolean;
}

export function describeSignals({ yelling, profanity, anguish, negation, blame, repeated }: PromptSignals) {
  const counts = { Yelling: yelling, Profanity: profanity, Anguish: anguish, Negation: negation, Blame: blame };
  const labels = Object.entries(counts).filter(([, count]) => count > 0).map(([name, count]) => `${name} ${formatInteger(count)}`);
  if (repeated) labels.push("Repeated");
  return labels.join(", ");
}
