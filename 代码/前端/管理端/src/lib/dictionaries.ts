export type DictionaryKey = "server" | "gender" | "level" | "kind" | "keyword";
export interface DictionaryOption {
  value: string;
  label: string;
  enabled: boolean;
  sort_order: number;
}
export interface DictionaryGroup {
  key: DictionaryKey;
  label: string;
  version: number;
  options: DictionaryOption[];
}
export const fixedProductFields: {
  key: DictionaryKey;
  label: string;
  required: boolean;
}[] = [
  { key: "server", label: "所在区服", required: true },
  { key: "gender", label: "角色性别", required: true },
  { key: "level", label: "VIP 等级", required: false },
];

export const dictionarySettingsFields = [
  { key: "keyword" as const, label: "热门筛选", required: false },
  ...fixedProductFields,
  { key: "kind" as const, label: "账号结构（历史字典）", required: false },
];

export function addDictionaryOption(
  group: DictionaryGroup,
  label: string,
): DictionaryGroup {
  const value = label.trim();
  if (!value || Array.from(value).length > 80)
    throw new Error("选项名称需为 1 至 80 个字。");
  if (group.options.length >= 500) throw new Error("每组最多保留 500 个选项。");
  if (
    group.options.some(
      (option) => option.value === value || option.label === value,
    )
  )
    throw new Error("已有同名选项，请编辑或启用原选项。");
  return {
    ...group,
    options: [
      ...group.options,
      { value, label: value, enabled: true, sort_order: group.options.length },
    ],
  };
}

export function validateDictionary(group: DictionaryGroup) {
  if (
    (group.key === "server" || group.key === "gender") &&
    !group.options.some((option) => option.enabled)
  )
    throw new Error(
      "区服和角色性别至少需要保留一个可用选项，否则无法发布商品。",
    );
  if (
    group.options.some(
      (option) =>
        !option.label.trim() ||
        Array.from(option.label.trim()).length > 80 ||
        /[\u0000-\u001f\u007f-\u009f]/u.test(option.label) ||
        (group.key === "keyword" && option.label !== option.label.trim()),
    )
  )
    throw new Error(
      "每个选项名称需为 1 至 80 个有效字符，关键词首尾不能留空格。",
    );
}
