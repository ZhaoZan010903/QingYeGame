import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { DictionaryOptions } from "../components/DictionarySettings";
import {
  addDictionaryOption,
  fixedProductFields,
  validateDictionary,
  type DictionaryGroup,
} from "./dictionaries";

describe("R21-1 热门筛选管理", () => {
  const keywords: DictionaryGroup = {
    key: "keyword",
    label: "热门筛选",
    version: 1,
    options: [],
  };

  it("热门词支持清空、新增与删除，固定发布字段不出现热门词", () => {
    expect(() => validateDictionary(keywords)).not.toThrow();
    const group = addDictionaryOption(keywords, "  永夜头  ");
    expect(group.options[0]).toEqual({
      value: "永夜头",
      label: "永夜头",
      enabled: true,
      sort_order: 0,
    });
    const html = renderToStaticMarkup(
      <DictionaryOptions group={group} query="" onChange={() => {}} />,
    );
    expect(html).toContain("删除关键词永夜头");
    expect(html).toContain("下移永夜头");
    expect(fixedProductFields.map((field) => field.key)).toEqual([
      "server",
      "gender",
      "level",
      "kind",
    ]);
    expect(() => addDictionaryOption(group, "永夜头")).toThrow("同名");
    expect(() =>
      validateDictionary({
        ...group,
        options: [{ ...group.options[0], label: "多行\n关键词" }],
      }),
    ).toThrow("有效字符");
  });

  it("普通字典保留停用操作，不提供会破坏历史引用的关键词删除按钮", () => {
    const group = addDictionaryOption(
      { ...keywords, key: "server" },
      "一舞时光",
    );
    const html = renderToStaticMarkup(
      <DictionaryOptions group={group} query="" onChange={() => {}} />,
    );
    expect(html).not.toContain("删除关键词");
    expect(html).toContain("可选");
  });
});
