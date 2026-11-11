export type PublishFieldKey =
  "title" | "description" | "custom_tags" | "private_credentials";
export interface PublishField {
  label: string;
  placeholder: string;
  required: boolean;
  enabled: boolean;
}
export interface PublishFormConfig {
  fields: Record<PublishFieldKey, PublishField>;
}
export const defaultPublishForm: PublishFormConfig = {
  fields: {
    title: {
      label: "账号标题",
      placeholder: "例如：月白 · 典藏衣橱 / 华东一区",
      required: true,
      enabled: true,
    },
    description: {
      label: "账号介绍",
      placeholder:
        "介绍服饰、典藏和需要说明的情况。私密账号信息请填写下方专用区域。",
      required: true,
      enabled: true,
    },
    custom_tags: {
      label: "自定义标签",
      placeholder: "例如：典藏 ×12、稀有翅膀",
      required: false,
      enabled: true,
    },
    private_credentials: {
      label: "账号交接资料",
      placeholder: "仅供平台授权管理员核验，不在商品页面公开。",
      required: false,
      enabled: true,
    },
  },
};
export function resolvePublishForm(input: unknown): PublishFormConfig {
  const fields = (input as Partial<PublishFormConfig> | null)?.fields;
  return {
    fields: Object.fromEntries(
      Object.entries(defaultPublishForm.fields).map(([key, base]) => {
        const next = fields?.[key as PublishFieldKey];
        return [
          key,
          {
            label:
              typeof next?.label === "string" && next.label.trim()
                ? next.label
                : base.label,
            placeholder:
              typeof next?.placeholder === "string"
                ? next.placeholder
                : base.placeholder,
            required:
              key === "title" || key === "description" || !!next?.required,
            enabled:
              key === "title" ||
              key === "description" ||
              next?.enabled !== false,
          },
        ];
      }),
    ) as Record<PublishFieldKey, PublishField>,
  };
}
