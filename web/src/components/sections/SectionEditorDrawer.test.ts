import { describe, expect, it } from "vitest";
import { buildAdminSectionPayload, buildProfileSectionSaveEntry } from "./SectionEditorDrawer";
import { queryDefinitionFromSectionConfig } from "@/api/types";

describe("SectionEditorDrawer payload builders", () => {
  it("preserves continue listening config for admin sections", () => {
    const payload = buildAdminSectionPayload({
      section: null,
      scope: "home",
      currentLibraryId: null,
      sectionType: "continue_watching",
      title: "Continue Listening",
      itemLimit: 20,
      featured: false,
      enabled: true,
      queryDefinition: queryDefinitionFromSectionConfig(),
      selectedCollectionId: "",
      recipeParams: { continue_type: "listening" },
    });

    expect(payload).toMatchObject({
      section_type: "continue_watching",
      title: "Continue Listening",
      config: { continue_type: "listening" },
    });
  });

  it("preserves continue listening config for profile sections", () => {
    const entry = buildProfileSectionSaveEntry({
      section: null,
      sectionType: "continue_watching",
      title: "Continue Listening",
      itemLimit: 20,
      featured: false,
      queryDefinition: queryDefinitionFromSectionConfig(),
      selectedCollectionId: "",
      recipeParams: { continue_type: "listening" },
    });

    expect(entry).toMatchObject({
      section_type: "continue_watching",
      title: "Continue Listening",
      is_custom: true,
      config: { continue_type: "listening" },
    });
  });

  it("replaces a generated row's legacy library filter in admin sections", () => {
    const section = {
      id: "1",
      scope: "home",
      title: "Recently Added in TV",
      section_type: "recently_added",
      item_limit: 20,
      featured: false,
      enabled: true,
      position: 0,
      config: { filter_library_id: 4, generated_source: "library" },
    };
    const payload = buildAdminSectionPayload({
      section: section as unknown as Parameters<typeof buildAdminSectionPayload>[0]["section"],
      scope: "home",
      currentLibraryId: null,
      sectionType: "recently_added",
      title: "Recently Added in TV",
      itemLimit: 20,
      featured: false,
      enabled: true,
      queryDefinition: queryDefinitionFromSectionConfig(),
      selectedCollectionId: "",
      recipeParams: { generated_source: "library", filter_library_ids: [6] },
    });

    expect(payload.config).toEqual({ generated_source: "library", filter_library_ids: [6] });
  });

  it("replaces a generated row's legacy library filter in profile sections", () => {
    const entry = buildProfileSectionSaveEntry({
      section: {
        id: "s1",
        section_type: "recently_added",
        title: "Recently Added in TV",
        featured: false,
        item_limit: 20,
        hidden: false,
        is_custom: false,
        customized: false,
        position: 0,
        config: { filter_library_id: 4 },
      },
      sectionType: "recently_added",
      title: "Recently Added in TV",
      itemLimit: 20,
      featured: false,
      queryDefinition: queryDefinitionFromSectionConfig(),
      selectedCollectionId: "",
      recipeParams: { filter_library_ids: [6] },
    });

    expect(entry.config).toEqual({ filter_library_ids: [6] });
  });
});
