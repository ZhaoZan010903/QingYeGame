import { idempotentRequest, imagePageFrom, productFrom, request } from "./api";
import type { Product, ProductPage, User } from "../types";
import type { DictionaryGroup, DictionaryKey } from "./dictionaries";

export type AdminRecord = Record<string, any>;
const unwrap = (response: AdminRecord): AdminRecord =>
  response.data ?? response;
const rows = (response: AdminRecord): AdminRecord[] => {
  const value = unwrap(response);
  return Array.isArray(value) ? value : (value.items ?? []);
};
const path = (id: string) => `/admin/products/${encodeURIComponent(id)}`;
export const canManage = (user: User | null | undefined) =>
  user?.role === "admin";

/** Admin requests always reach the server; there is no local or fixture-backed admin mode. */
export const adminApi = {
  async dictionaries(): Promise<DictionaryGroup[]> {
    return rows(await request("/admin/dictionaries")) as DictionaryGroup[];
  },
  async saveDictionary(group: DictionaryGroup): Promise<DictionaryGroup> {
    return unwrap(
      await request(
        `/admin/dictionaries/${encodeURIComponent(group.key as DictionaryKey)}`,
        "PUT",
        {
          expected_version: group.version,
          options: group.options,
        },
      ),
    ) as DictionaryGroup;
  },
  async users(params: URLSearchParams) {
    const response = await request(`/admin/users?${params}`);
    return {
      items: rows(response),
      total: response.total ?? 0,
      nextCursor: response.next_cursor ?? null,
    };
  },
  async setUserEnabled(id: string, enabled: boolean) {
    await request(`/admin/users/${encodeURIComponent(id)}`, "PATCH", {
      enabled,
    });
  },
  async recommendations(refresh = false): Promise<AdminRecord> {
    return request(
      `/admin/recommendations${refresh ? "/refresh" : ""}`,
      refresh ? "POST" : "GET",
    );
  },
  async products(params: URLSearchParams): Promise<ProductPage> {
    const result = await request(`/admin/products?${params}`);
    return {
      items: rows(result).map(productFrom),
      total: result.total ?? unwrap(result).total ?? 0,
      nextCursor: result.next_cursor ?? unwrap(result).next_cursor ?? null,
    };
  },
  async product(id: string): Promise<AdminRecord> {
    return unwrap(await request(path(id)));
  },
  async trashProduct(id: string, version: number, reason: string) {
    return request(path(id), "DELETE", { expected_version: version, reason });
  },
  async restoreProduct(id: string, version: number) {
    return request(`${path(id)}/restore`, "POST", {
      expected_version: version,
    });
  },
  async privateCredentials(
    id: string,
  ): Promise<{ qq_account: string; qq_password: string }> {
    return unwrap(await request(`${path(id)}/private-credentials`)) as {
      qq_account: string;
      qq_password: string;
    };
  },
  async saveProduct(
    input: AdminRecord,
    id?: string,
    version?: number,
  ): Promise<AdminRecord> {
    return unwrap(
      id
        ? await request(path(id), "PATCH", {
            ...input,
            expected_version: version,
          })
        : await idempotentRequest("/admin/products", input),
    );
  },
  async action(
    id: string,
    action: "review" | "status" | "publish",
    body: AdminRecord,
  ) {
    await request(`${path(id)}/${action}`, "POST", body);
    return this.product(id);
  },
  async featured(id: string, rank: number | null) {
    await request(`${path(id)}/featured`, "PUT", { manual_rank: rank });
  },
  async list(resource: "categories" | "contacts" | "pages" | "settings") {
    return rows(await request(`/admin/${resource}`));
  },
  async save(
    resource: "categories" | "contacts" | "pages" | "settings",
    id: string | undefined,
    input: AdminRecord,
  ) {
    return unwrap(
      await request(
        `/admin/${resource}${id ? `/${encodeURIComponent(id)}` : ""}`,
        id ? "PUT" : "POST",
        input,
      ),
    );
  },
};

// ImageUploader accepts this transport while retaining its queue, version checks and retry IDs.
export const adminImageApi = {
  async myProduct(id: string): Promise<Product> {
    return productFrom(await adminApi.product(id));
  },
  async myImages(id: string, cursor?: string | null, version?: number) {
    const params = new URLSearchParams({ limit: "20" });
    if (cursor) params.set("cursor", cursor);
    if (version) params.set("expected_version", String(version));
    return imagePageFrom(await request(`${path(id)}/images?${params}`));
  },
  async appendImages(id: string, mediaIds: string[], version: number) {
    await idempotentRequest(`${path(id)}/images`, {
      media_ids: mediaIds,
      expected_version: version,
    });
    return this.myProduct(id);
  },
  async removeImage(id: string, mediaId: string, version: number) {
    await request(
      `${path(id)}/images/${encodeURIComponent(mediaId)}`,
      "DELETE",
      {
        expected_version: version,
      },
    );
    return this.myProduct(id);
  },
  async moveImage(
    id: string,
    mediaId: string,
    beforeId: string | null,
    version: number,
  ) {
    await request(
      `${path(id)}/images/${encodeURIComponent(mediaId)}`,
      "PATCH",
      {
        before_media_id: beforeId,
        expected_version: version,
      },
    );
    return this.myProduct(id);
  },
};
