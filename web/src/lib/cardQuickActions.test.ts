import { describe, expect, it } from "vitest";

import { CARD_QUICK_ACTION_MODES, CARD_QUICK_ACTION_OPTIONS } from "@/lib/cardQuickActions";
import { SETTING_DEFINITIONS, SETTING_KEYS } from "@/lib/settingsContract";

// The literals here exist only because the generated definition types its
// members as unknown, which cannot produce the literal union the card props
// need. These assertions are what keeps the hand-written copy honest.
describe("card quick action modes", () => {
  const members = SETTING_DEFINITIONS[SETTING_KEYS.UI_CARD_QUICK_ACTIONS].values ?? [];

  it("lists exactly the contract's enum members, in manifest order", () => {
    expect(CARD_QUICK_ACTION_MODES).toEqual(members.map((member) => member.value));
  });

  it("labels each option the way the contract labels it", () => {
    expect(CARD_QUICK_ACTION_OPTIONS).toEqual(
      members.map((member) => ({ value: member.value, label: member.label })),
    );
  });
});
