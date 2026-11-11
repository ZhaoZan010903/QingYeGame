import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { DemoContent, guides, type DemoKind } from "./AdminGuide";
import { HomeDesigner } from "./HomeDesigner";
import { DictionaryOptions } from "./DictionarySettings";
import {
  readHomePresentation,
  resolveHomePresentation,
} from "../lib/home-content";
import { makeCode, parseFilters, updateAttribute } from "../lib/admin-ux";

afterEach(() => vi.unstubAllGlobals());
describe("管理后台操作辅助", () => {
  it("HTTP 环境没有 randomUUID 时仍可创建分类和筛选编号", () => {
    vi.stubGlobal("crypto", {
      getRandomValues: (value: Uint8Array) => {
        value.fill(12);
        return value;
      },
    });
    expect(makeCode("category")).toMatch(/^category-[a-f0-9]{20}$/);
  });
  it("普通属性编辑保留未展示的迁移资料与其他选项", () => {
    const value = updateAttribute(
      JSON.stringify({
        server: ["old"],
        custom: { note: "保留" },
        legacy: [1, 2],
      }),
      "server",
      ["new", "east"],
    );
    expect(JSON.parse(value)).toEqual({
      server: ["new", "east"],
      custom: { note: "保留" },
      legacy: [1, 2],
    });
  });
  it.each(["null", "[]", "broken"])(
    "损坏的属性 %s 不会静默变成空对象",
    (raw) => {
      expect(() => updateAttribute(raw, "server", "new")).toThrow();
    },
  );
  it("共享字典保留固定标识，允许标签改名与停用", () => {
    const html = renderToStaticMarkup(
      <DictionaryOptions
        group={{
          key: "server",
          label: "所在区服",
          version: 1,
          options: [
            { value: "east", label: "华东", enabled: false, sort_order: 0 },
          ],
        }}
        query=""
        onChange={() => {}}
      />,
    );
    expect(html).toContain("华东");
    expect(html).toContain("east");
    expect(html).toContain("已停用");
    expect(html).not.toContain("添加筛选分组");
  });
  it("未知旧筛选格式不会被悄悄丢弃", () => {
    expect(() => parseFilters('{"filters":[]}')).toThrow();
  });
  it.each(Object.keys(guides) as DemoKind[])(
    "%s 演示只显示练习内容，不请求接口",
    (kind) => {
      const fetch = vi.fn(() => {
        throw new Error("demo must not call API");
      });
      vi.stubGlobal("fetch", fetch);
      const html = renderToStaticMarkup(
        <MemoryRouter>
          <DemoContent kind={kind} />
        </MemoryRouter>,
      );
      expect(html).toContain("不会保存到网站");
      expect(html).toContain(guides[kind].title);
      expect(fetch).not.toHaveBeenCalled();
    },
  );
  it("首页示意读取输入文案，转义文本并不把预览自动保存", () => {
    const home = resolveHomePresentation();
    home.editorial.title = "<script>alert(1)</script>";
    const change = vi.fn();
    const html = renderToStaticMarkup(
      <MemoryRouter>
        <HomeDesigner value={home} onChange={change} />
      </MemoryRouter>,
    );
    expect(html).toContain("&lt;script&gt;");
    expect(html).not.toContain("<script>");
    expect(change).not.toHaveBeenCalled();
  });
  it("显式隐藏热门和快捷入口不被默认值重新填充", () => {
    const home = readHomePresentation({
      ...resolveHomePresentation(),
      sections: [],
      shortcuts: [],
    });
    expect(home.sections).toEqual([]);
    expect(home.shortcuts).toEqual([]);
  });
});
