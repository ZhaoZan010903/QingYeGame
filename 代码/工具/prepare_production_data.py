#!/usr/bin/env python3
"""Prepare a private, allowlisted initial-production data package.

Export is one read-only snapshot of local qingye_test. Import previews by
default and only applies to a fresh, migrated local qingye_production database
with its independently seeded administrator and media workers disabled.
No source account, session, chat, statistic, job or storage replica is imported.
"""
from __future__ import annotations

import argparse
import base64
from collections import Counter
from decimal import Decimal
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit

FORMAT = "qingye-production-data-r23-v2"
OWNER = "__production_administrator__"
MAX_PACKAGE_BYTES = 256 * 1024 * 1024
SETTING_KEYS = {"home_presentation", "recommendation", "publish_form"}
TARGET_DATABASES = {"qingye_production", "qingye_production_validation"}
DATABASE_ROUTES = {
    "qingye_test": ("psql", "/var/run/postgresql", 5432),
    "qingye_production_validation": ("psql", "/var/run/postgresql", 5432),
    "qingye_production": ("/usr/pgsql-16/bin/psql", "/run/qingye-production-postgres", 5433),
}


def database_route(database, validation_instance=None):
    if database == "qingye_production_validation":
        if validation_instance not in {"test", "production"}:
            raise ValueError("validation database requires an explicit test or production instance")
        return DATABASE_ROUTES["qingye_test" if validation_instance == "test" else "qingye_production"]
    if database not in DATABASE_ROUTES or validation_instance is not None:
        raise ValueError("unsupported database instance")
    return DATABASE_ROUTES[database]
KEY_RE = re.compile(r"(?:legacy/[a-f0-9]{64}|images/[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}\.(?:jpg|jpeg|png|webp))\Z")
SHA_RE = re.compile(r"[a-f0-9]{64}\Z")
ADMIN_RE = re.compile(r"[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}\Z")

# Explicit columns ensure a future schema change requires a new reviewed tool.
COLUMNS = {
    "categories": "id name trade_type price_units filter_config sort_order enabled".split(),
    "product_dictionary_groups": "key label version sort_order".split(),
    "product_dictionary_options": "dictionary_key value label enabled sort_order".split(),
    "products": "id legacy_id product_code owner_id category_id title trade_type price_amount price_unit attributes description status source version approved_version manual_rank review_note legacy_price legacy_category legacy_payload verification_status created_at updated_at published_at rental_quotes deleted_at draft_expires_at last_media_upload_at deleted_by_admin rental_category purged_at recommendation_boost recommendation_version".split(),
    "media_files": "id owner_id filename mime_type byte_size storage_key source_url status upload_token_hash upload_expires_at sha256 created_at upload_audience managed_upload ready_at deleted_at purpose conversation_id resumable lifecycle_managed upload_cleanup_requested upload_offset upload_security_version processing_token processing_until".split(),
    "product_images": "product_id media_id sort_order".split(),
    "product_private_credentials": "product_id ciphertext updated_at".split(),
    "contacts": "id name purpose channel contact_value qr_media_id qr_url hours enabled".split(),
    "content_pages": "slug title body published version".split(),
    "site_settings": "key value updated_at cache_revision".split(),
    "avatar_library": "id name media_id sort_order enabled version created_by created_at updated_at".split(),
}
PRIMARY_KEYS = {
    "categories": ["id"], "product_dictionary_groups": ["key"],
    "product_dictionary_options": ["dictionary_key", "value"], "products": ["id"],
    "media_files": ["id"], "product_images": ["product_id", "media_id"],
    "product_private_credentials": ["product_id"], "contacts": ["id"],
    "content_pages": ["slug"], "site_settings": ["key"], "avatar_library": ["id"],
}
ALL_TABLES = sorted(set(COLUMNS) | set("admin_account_requests admin_logs chat_conversations chat_message_images chat_messages chat_presence favorites idempotency media_gc_queue media_legacy_replicas media_object_cleanup media_object_moves media_objects media_permanent_cleanup media_retired_keys media_stage_candidates nickname_requests product_consultations product_hot_scores product_item_similarities product_reviews product_upload_restrictions product_views_daily recommendation_preferences registration_consents schema_migrations sessions sms_challenges users".split()))
MIGRATIONS = [
    "0001_confirm_legacy_price_units.sql", "0002_product_chat.sql", "0003_personal_recommendations.sql",
    "0004_itemcf.sql", "0005_product_workflows.sql", "0006_customer_consultations.sql",
    "0007_shared_dictionaries_and_drafts.sql", "0008_home_cache.sql", "0009_admin_product_trash.sql",
    "0010_chat_query_indexes.sql", "0011_chat_images_and_avatars.sql", "0012_product_media_lifecycle.sql",
    "0013_registration_sms.sql", "0014_sms_login.sql", "0015_member_profile_review.sql",
    "0016_catalog_hot_keywords.sql", "0017_rental_quote_periods.sql", "0018_product_permanent_delete.sql",
    "0019_admin_accounts.sql", "0020_product_recommendation_boost.sql",
]


def encoded(value):
    """Canonical JSON without rounding numeric money or nested JSON decimals."""
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, str):
        return json.dumps(value, ensure_ascii=False)
    if isinstance(value, int):
        return str(value)
    if isinstance(value, Decimal) and value.is_finite():
        return format(value, "f")
    if isinstance(value, list):
        return "[" + ",".join(encoded(item) for item in value) + "]"
    if isinstance(value, dict) and all(isinstance(key, str) for key in value):
        return "{" + ",".join(encoded(key) + ":" + encoded(value[key]) for key in sorted(value)) + "}"
    raise ValueError("unsupported package value")


def decoded(value):
    return json.loads(value, parse_float=Decimal, parse_constant=lambda _: (_ for _ in ()).throw(ValueError("nonfinite JSON")))


def digest(value):
    result = hashlib.sha256()
    for chunk in encoded_chunks(value):
        result.update(chunk.encode())
    return result.hexdigest()


def encoded_chunks(value):
    """Canonical JSON chunks avoid a second full private package in memory."""
    if isinstance(value, list):
        yield "["
        for index, item in enumerate(value):
            if index:
                yield ","
            yield from encoded_chunks(item)
        yield "]"
    elif isinstance(value, dict) and all(isinstance(key, str) for key in value):
        yield "{"
        for index, key in enumerate(sorted(value)):
            if index:
                yield ","
            yield encoded(key) + ":"
            yield from encoded_chunks(value[key])
        yield "}"
    else:
        yield encoded(value)


def literal(value):
    return "'" + value.replace("'", "''") + "'"


def schema_query():
    return """SELECT coalesce(jsonb_agg(jsonb_build_object('table',table_name,'column',column_name,
      'position',ordinal_position,'type',udt_name,'nullable',is_nullable,'default',column_default)
      ORDER BY table_name,ordinal_position),'[]') FROM information_schema.columns WHERE table_schema='public'"""


def rows_query(table, where="true"):
    columns = ",".join(COLUMNS[table])
    return f"SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]') FROM (SELECT {columns} FROM {table} WHERE {where}) r"


def validation_snapshot_statement():
    """Hash full target state without aggregating private rows into one value."""
    values = []
    for table in ALL_TABLES:
        values.append(literal(table) + f""",(SELECT jsonb_build_object('count',count(*),'sha256',
 encode(sha256(convert_to(coalesce(string_agg(h,E'\\n' ORDER BY h),''),'UTF8')),'hex'))
 FROM (SELECT encode(sha256(convert_to(to_jsonb(t)::text,'UTF8')),'hex') h FROM {table} t) rows)""")
    for sequence in ("product_sale_code_seq", "product_rent_code_seq"):
        # Sequences do not support a whole-row composite reference (SQLSTATE 42809).
        values.append(literal(sequence) + f",(SELECT jsonb_build_object('last_value',last_value,'is_called',is_called) FROM {sequence})")
    return """BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout='30s'; SET LOCAL TIME ZONE 'UTC';
SET LOCAL search_path=pg_catalog,public; SET LOCAL work_mem='4MB';
SELECT jsonb_build_object(""" + ",".join(values) + ");\nCOMMIT;"


def export_statement():
    conditions = {
        "products": "id IN(SELECT id FROM chosen)",
        "product_images": "product_id IN(SELECT id FROM chosen)",
        "product_private_credentials": "product_id IN(SELECT id FROM chosen)",
        "media_files": "id IN(SELECT id FROM needed_media)",
        "site_settings": "key IN('home_presentation','recommendation','publish_form')",
    }
    tables = ",".join(literal(table) + ",(" + rows_query(table, conditions.get(table, "true")) + ")" for table in COLUMNS)
    return f"""BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout='120s'; SET LOCAL TIME ZONE 'UTC'; SET LOCAL search_path=pg_catalog,public;
WITH chosen AS(SELECT p.id FROM products p JOIN categories c ON c.id=p.category_id
 WHERE p.deleted_at IS NULL AND p.status='published' AND p.approved_version=p.version AND c.enabled),
needed_media AS(SELECT media_id id FROM product_images WHERE product_id IN(SELECT id FROM chosen)
 UNION SELECT media_id FROM avatar_library UNION SELECT qr_media_id FROM contacts WHERE qr_media_id IS NOT NULL)
SELECT jsonb_build_object('source_database',current_database(),'migrations',(SELECT jsonb_agg(version ORDER BY version) FROM schema_migrations),
 'schema',({schema_query()}),'tables',jsonb_build_object({tables}),
 'checks',jsonb_build_object(
 'managed_objects',(SELECT count(*) FROM media_objects WHERE media_id IN(SELECT id FROM needed_media)),
 'retired_keys',(SELECT count(*) FROM media_files m JOIN media_retired_keys k USING(storage_key) WHERE m.id IN(SELECT id FROM needed_media)),
 'unverified_product_replicas',(SELECT count(*) FROM product_images pi JOIN media_files m ON m.id=pi.media_id
 LEFT JOIN media_legacy_replicas r ON r.media_id=m.id WHERE pi.product_id IN(SELECT id FROM chosen)
 AND (r.media_id IS NULL OR r.deleted_at IS NOT NULL OR r.storage_key<>m.storage_key OR r.sha256<>m.sha256
 OR r.byte_size<>m.byte_size OR r.object_key<>'qingye-test/media/'||m.storage_key))));
COMMIT;"""


def prepare_bundle(source, expected_products=576, credential_key_sha256=None):
    if source.get("source_database") != "qingye_test" or source.get("migrations") != MIGRATIONS:
        raise ValueError("source database or migration versions differ from the reviewed baseline")
    if source.get("checks") != {"managed_objects": 0, "retired_keys": 0, "unverified_product_replicas": 0}:
        raise ValueError("source media needs separate lifecycle or replica reconciliation")
    tables = {table: [dict(row) for row in rows] for table, rows in source["tables"].items()}
    if set(tables) != set(COLUMNS):
        raise ValueError("unexpected export table set")
    for row in tables["products"]:
        row.update(owner_id=OWNER, review_note="", draft_expires_at=None, last_media_upload_at=None)
    for row in tables["media_files"]:
        row.update(owner_id=OWNER, upload_token_hash=None, upload_expires_at=None, conversation_id=None,
                   processing_token=None, processing_until=None, resumable=False,
                   upload_cleanup_requested=False, upload_offset=0, upload_security_version=0)
    for row in tables["avatar_library"]:
        row["created_by"] = OWNER
    for row in tables["content_pages"]:
        if row["slug"] in {"terms", "privacy"}:
            row["published"] = False
    for rows in tables.values():
        rows.sort(key=encoded)
    payload = {"format": FORMAT, "source_database": "qingye_test", "migrations": source["migrations"],
               "credential_key_sha256": credential_key_sha256 if tables["product_private_credentials"] else None,
               "image_order_sha256": None,
               "schema": source["schema"], "tables": tables}
    bundle = {"payload": payload, "sha256": digest(payload)}
    validate_bundle(bundle, expected_products)
    return bundle


def verify_support_files(source, media_directory):
    """R19 avatar/contact rows may lack SHA256; bind their actual private files."""
    root = media_directory.resolve(strict=True)
    if not root.is_dir():
        raise ValueError("private source media directory required")
    selected = {row["media_id"] for row in source["tables"]["product_images"]}
    for row in source["tables"]["media_files"]:
        if row["id"] in selected:
            continue
        key = row["storage_key"]
        if not KEY_RE.fullmatch(key) or not 0 < row["byte_size"] <= 64 * 1024 * 1024:
            raise ValueError("unsafe support media key or size")
        path = root / key
        resolved = path.resolve(strict=True)
        if path.is_symlink() or root not in resolved.parents or not resolved.is_file():
            raise ValueError("support media escapes private source directory")
        before = resolved.stat()
        if before.st_size != row["byte_size"]:
            raise ValueError("support media size differs from source database")
        sha = hashlib.sha256()
        with resolved.open("rb") as handle:
            while data := handle.read(1024 * 1024):
                sha.update(data)
            after = os.fstat(handle.fileno())
        identity = lambda info: (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns)
        if identity(before) != identity(after) or identity(before) != identity(resolved.stat()):
            raise ValueError("support media changed while hashing")
        if row["sha256"] not in {None, "", sha.hexdigest()}:
            raise ValueError("support media digest differs from source database")
        row["sha256"] = sha.hexdigest()
    return source


def validate_bundle(bundle, expected_products=576):
    if set(bundle) != {"payload", "sha256"} or not SHA_RE.fullmatch(bundle.get("sha256", "")) or digest(bundle["payload"]) != bundle["sha256"]:
        raise ValueError("package checksum mismatch")
    payload = bundle["payload"]
    if set(payload) != {"format", "source_database", "migrations", "schema", "tables", "credential_key_sha256", "image_order_sha256"} or payload["format"] != FORMAT or payload["source_database"] != "qingye_test" or payload["migrations"] != MIGRATIONS:
        raise ValueError("unsupported package format or source")
    if payload["image_order_sha256"] is not None and not SHA_RE.fullmatch(payload["image_order_sha256"]):
        raise ValueError("invalid image-order fingerprint")
    tables = payload["tables"]
    if set(tables) != set(COLUMNS):
        raise ValueError("unexpected package table set")
    schema = payload["schema"]
    if sorted({row["table"] for row in schema}) != ALL_TABLES:
        raise ValueError("unexpected schema table set")
    for table, columns in COLUMNS.items():
        if [row["column"] for row in schema if row["table"] == table] != columns:
            raise ValueError("unsupported source column layout")
        if not isinstance(tables[table], list) or any(set(row) != set(columns) for row in tables[table]):
            raise ValueError("unsupported package column set")
    products = keyed(tables["products"], "id")
    categories = keyed(tables["categories"], "id")
    media = keyed(tables["media_files"], "id")
    if len(products) != expected_products:
        raise ValueError("visible product count changed")
    if expected_products == 576 and Counter(p["trade_type"] for p in products.values()) != {"sale": 45, "rent": 531}:
        raise ValueError("visible product trade counts changed")
    for row in products.values():
        if row["status"] != "published" or row["version"] != row["approved_version"] or row["deleted_at"] is not None or row["purged_at"] is not None or row["deleted_by_admin"] or not categories.get(row["category_id"], {}).get("enabled"):
            raise ValueError("package contains a non-visible product")
        if row["owner_id"] != OWNER or row["review_note"] != "" or row["draft_expires_at"] is not None or row["last_media_upload_at"] is not None:
            raise ValueError("package retains source ownership or workflow state")
    image_ids, product_media, references = set(), {}, set()
    for row in tables["product_images"]:
        mid, pid = row["media_id"], row["product_id"]
        if pid not in products or mid not in media or mid in image_ids or not isinstance(row["sort_order"], int):
            raise ValueError("invalid or duplicate ordered product image")
        image_ids.add(mid)
        product_media.setdefault(pid, set()).add(mid)
        references.add(mid)
        if media[mid]["purpose"] != "product":
            raise ValueError("invalid product image purpose")
    if set(product_media) != set(products):
        raise ValueError("product without images")
    keyed(tables["avatar_library"], "id")
    for row in tables["avatar_library"]:
        if row["created_by"] != OWNER or row["media_id"] not in media or media[row["media_id"]]["purpose"] != "avatar":
            raise ValueError("invalid avatar-library reference")
        references.add(row["media_id"])
    keyed(tables["contacts"], "id")
    for row in tables["contacts"]:
        mid = row["qr_media_id"]
        if mid is not None:
            if mid not in media or media[mid]["purpose"] not in {"product", "contact"}:
                raise ValueError("invalid contact-media reference")
            references.add(mid)
    if references != set(media):
        raise ValueError("unreferenced media included in package")
    keys = set()
    for row in media.values():
        key = row["storage_key"]
        if not KEY_RE.fullmatch(key) or key in keys or row["status"] != "ready" or row["deleted_at"] is not None or row["lifecycle_managed"] or not SHA_RE.fullmatch(row["sha256"] or "") or row["byte_size"] <= 0:
            raise ValueError("media requires separate integrity or lifecycle reconciliation")
        keys.add(key)
        if row["owner_id"] != OWNER or any(row[x] is not None for x in ("upload_token_hash", "upload_expires_at", "conversation_id", "processing_token", "processing_until")) or any(row[x] for x in ("resumable", "upload_cleanup_requested", "upload_offset", "upload_security_version")):
            raise ValueError("media retains test upload capability or processing state")
    for row in tables["product_private_credentials"]:
        if row["product_id"] not in products or not re.fullmatch(r"\\x(?:[0-9a-f]{2})+", row["ciphertext"]):
            raise ValueError("invalid private credential record")
    keyed(tables["product_private_credentials"], "product_id")
    fingerprint = payload["credential_key_sha256"]
    if tables["product_private_credentials"]:
        if not isinstance(fingerprint, str) or not SHA_RE.fullmatch(fingerprint):
            raise ValueError("encrypted private credentials require their source key fingerprint")
    elif fingerprint is not None:
        raise ValueError("unexpected credential key fingerprint")
    pages = keyed(tables["content_pages"], "slug")
    if any(pages.get(slug, {}).get("published", False) for slug in ("terms", "privacy")):
        raise ValueError("test registration agreements cannot be published in production")
    settings = keyed(tables["site_settings"], "key")
    if not set(settings) <= SETTING_KEYS:
        raise ValueError("runtime recommendation state included")
    for slide in settings.get("home_presentation", {}).get("value", {}).get("slides", []):
        pid, mid = slide.get("productId"), slide.get("imageId")
        if pid not in products or (mid and mid not in product_media[pid]):
            raise ValueError("home slide references an excluded product or image")
    groups = keyed(tables["product_dictionary_groups"], "key")
    option_ids = set()
    for row in tables["product_dictionary_options"]:
        identity = (row["dictionary_key"], row["value"])
        if identity in option_ids or row["dictionary_key"] not in groups:
            raise ValueError("invalid dictionary reference")
        option_ids.add(identity)
    return bundle


def keyed(rows, field):
    result = {row[field]: row for row in rows}
    if len(result) != len(rows) or any(not isinstance(key, str) or not key for key in result):
        raise ValueError("invalid or duplicate row identity")
    return result


def summary(bundle):
    tables = bundle["payload"]["tables"]
    selected = {row["media_id"] for row in tables["product_images"]}
    return {"format": FORMAT, "sha256": bundle["sha256"], "image_order_sha256": bundle["payload"]["image_order_sha256"],
            "tables": {name: {"count": len(rows), "sha256": digest(rows)} for name, rows in tables.items()},
            "trade_counts": dict(Counter(row["trade_type"] for row in tables["products"])),
            "product_image_bytes": sum(row["byte_size"] for row in tables["media_files"] if row["id"] in selected),
            "local_support_files": sum(row["id"] not in selected for row in tables["media_files"]),
            "local_support_bytes": sum(row["byte_size"] for row in tables["media_files"] if row["id"] not in selected),
            "terms_privacy_published": False, "source_accounts_imported": 0, "media_jobs_imported": 0}


def bind_image_order(bundle, path):
    ensure_private(path)
    raw = path.read_bytes()
    ordered = [decoded(line) for line in raw.decode("utf-8").splitlines()]
    if any(set(row) != {"media_id", "product_id", "sort_order"} for row in ordered):
        raise ValueError("unexpected image-order manifest columns")
    expected = bundle["payload"]["tables"]["product_images"]
    if sorted(ordered, key=encoded) != sorted(expected, key=encoded):
        raise ValueError("product image association or order differs from verified image order")
    fingerprint = hashlib.sha256(raw).hexdigest()
    if bundle["payload"]["image_order_sha256"] not in {None, fingerprint}:
        raise ValueError("verified image-order manifest fingerprint differs")
    payload = dict(bundle["payload"], image_order_sha256=fingerprint)
    output = {"payload": payload, "sha256": digest(payload)}
    return output


def mapped_tables(bundle, administrator):
    if not ADMIN_RE.fullmatch(administrator):
        raise ValueError("explicit production administrator UUID required")
    tables = dict(bundle["payload"]["tables"])
    for table, column in (("products", "owner_id"), ("media_files", "owner_id"), ("avatar_library", "created_by")):
        tables[table] = [dict(row, **{column: administrator}) for row in tables[table]]
    return tables


def initial_dictionaries():
    groups = [{"key": key, "label": label, "version": 1, "sort_order": order} for key, label, order in
              (("server", "游戏区服", 1), ("gender", "角色性别", 2), ("level", "VIP等级", 3), ("kind", "账号结构", 4), ("keyword", "热门筛选", 0))]
    keywords = "600外搭以上,500外搭以上,包子,400外搭以上,300外搭以上,200外搭以上,永夜头,春日头,妄情头,狐惑外搭,墨离头,酒红头,永生头,宠溺头,纯黑名片,独角兽,妖瞳,冷焰,余烬,叫人,狐狸,极地,白骨".split(",")
    options = [{"dictionary_key": "keyword", "value": word, "label": word, "enabled": True, "sort_order": index + 1} for index, word in enumerate(keywords)]
    options += [{"dictionary_key": key, "value": value, "label": label, "enabled": True, "sort_order": order} for key, value, label, order in
                (("gender", "female", "女角色", 1), ("gender", "male", "男角色", 2), ("kind", "linked", "双号连体", 1), ("kind", "single", "独立账号", 2))]
    return {"product_dictionary_groups": groups, "product_dictionary_options": options}


def equal_table_guard(table, expression):
    return f"""IF EXISTS((SELECT * FROM {table} EXCEPT SELECT * FROM jsonb_populate_recordset(NULL::{table},{expression}))
 UNION ALL (SELECT * FROM jsonb_populate_recordset(NULL::{table},{expression}) EXCEPT SELECT * FROM {table}))
 THEN RAISE EXCEPTION 'target table differs: {table}'; END IF;"""


def do_block(statements, declaration=""):
    statements = re.sub(r"RAISE EXCEPTION '([^']+)';", lambda match:
                        "RAISE EXCEPTION " + literal(match[1]) + " USING ERRCODE=" + literal(guard_code(match[1])) + ";", statements)
    body = ("DECLARE " + declaration + "\n" if declaration else "") + "BEGIN\n" + statements + "\nEND;"
    delimiter = "$qingye_import_" + hashlib.sha256(body.encode()).hexdigest() + "$"
    while delimiter in body:
        delimiter = delimiter[:-1] + "x$"
    return "DO " + delimiter + "\n" + body + " " + delimiter + ";\n"


def guard_code(message):
    code = "Q" + hashlib.sha256(message.encode()).hexdigest()[:4].upper()
    return code[:-1] + "F" if code.endswith("000") else code


class DatabaseOperationError(RuntimeError):
    def __init__(self, sqlstate=None, guard=None):
        self.sqlstate, self.guard = sqlstate, guard
        label = guard or "database operation rejected"
        super().__init__(label + (" (SQLSTATE=" + sqlstate + ")" if sqlstate else "") + "; no business data was printed")


def row_batches(rows):
    # Bound JSON parsing and PL/pgSQL expression memory per statement.
    batch, size = [], 0
    for row in rows:
        row_size = len(encoded(row).encode())
        if row_size > 8 * 1024 * 1024:
            raise ValueError("individual import row exceeds reviewed size budget")
        if batch and (len(batch) >= 500 or size + row_size > 512 * 1024):
            yield batch
            batch, size = [], 0
        batch.append(row)
        size += row_size
    if batch:
        yield batch


def import_statement(bundle, administrator, apply=False, database="qingye_production", validation_instance=None):
    """Small-fixture convenience; real imports stream iter_import_statements."""
    return "\n".join(iter_import_statements(bundle, administrator, apply, database, validation_instance))


def iter_source_verification_statements(bundle):
    """Revalidate a private package against one read-only source snapshot."""
    tables = bundle["payload"]["tables"]
    selected = "SELECT p.id FROM products p JOIN categories c ON c.id=p.category_id WHERE p.deleted_at IS NULL AND p.status='published' AND p.approved_version=p.version AND c.enabled"
    conditions = {
        "products": "id IN(" + selected + ")",
        "product_images": "product_id IN(" + selected + ")",
        "product_private_credentials": "product_id IN(" + selected + ")",
        "media_files": "id IN(SELECT media_id FROM product_images WHERE product_id IN(" + selected + ") UNION SELECT media_id FROM avatar_library UNION SELECT qr_media_id FROM contacts WHERE qr_media_id IS NOT NULL)",
        "site_settings": "key IN('home_presentation','recommendation','publish_form')",
    }
    yield """BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout='120s'; SET LOCAL TIME ZONE 'UTC';
SET LOCAL search_path=pg_catalog,public; SET LOCAL work_mem='4MB';
SET LOCAL standard_conforming_strings=on;"""
    yield do_block(f"""IF current_database()<>'qingye_test' OR current_setting('port')::integer<>5432 THEN RAISE EXCEPTION 'wrong source database or instance'; END IF;
IF (SELECT jsonb_agg(version ORDER BY version) FROM schema_migrations) IS DISTINCT FROM {literal(encoded(MIGRATIONS))}::jsonb THEN RAISE EXCEPTION 'source migrations differ'; END IF;
IF ({schema_query()}) IS DISTINCT FROM {literal(encoded(bundle['payload']['schema']))}::jsonb THEN RAISE EXCEPTION 'source schema differs'; END IF;""")
    product_media_ids = {row["media_id"] for row in tables["product_images"]}
    support_ids = [row["id"] for row in tables["media_files"] if row["id"] not in product_media_ids]
    support_condition = "t.id IN(" + ",".join(map(literal, support_ids)) + ")" if support_ids else "false"
    media_overrides = {"owner_id": literal(OWNER), "upload_token_hash": "NULL::text", "upload_expires_at": "NULL::timestamptz",
                       "conversation_id": "NULL::text", "processing_token": "NULL::text", "processing_until": "NULL::timestamptz",
                       "resumable": "false", "upload_cleanup_requested": "false", "upload_offset": "0::bigint", "upload_security_version": "0::bigint",
                       "sha256": "CASE WHEN " + support_condition + " AND coalesce(t.sha256,'')='' THEN e.sha256 ELSE t.sha256 END"}
    overrides = {"products": {"owner_id": literal(OWNER), "review_note": "''::text", "draft_expires_at": "NULL::timestamptz", "last_media_upload_at": "NULL::timestamptz"},
                 "media_files": media_overrides, "avatar_library": {"created_by": literal(OWNER)},
                 "content_pages": {"published": "CASE WHEN t.slug IN('terms','privacy') THEN false ELSE t.published END"}}
    for table, columns in COLUMNS.items():
        yield do_block(f"IF (SELECT count(*) FROM {table} WHERE {conditions.get(table, 'true')})<>{len(tables[table])} THEN RAISE EXCEPTION 'source row count differs: {table}'; END IF;")
        identity = " AND ".join(f"t.{key}=e.{key}" for key in PRIMARY_KEYS[table])
        projection = ",".join(overrides.get(table, {}).get(column, "t." + column) + " AS " + column for column in columns)
        for batch in row_batches(tables[table]):
            yield do_block(f"""IF EXISTS(WITH expected_rows AS MATERIALIZED(SELECT * FROM jsonb_populate_recordset(NULL::{table},{literal(encoded(batch))}::jsonb))
SELECT * FROM expected_rows EXCEPT SELECT {projection} FROM {table} t JOIN expected_rows e ON {identity})
THEN RAISE EXCEPTION 'source table differs: {table}'; END IF;""")
    media_condition = conditions["media_files"]
    yield do_block(f"""IF EXISTS(SELECT 1 FROM media_objects WHERE media_id IN(SELECT id FROM media_files WHERE {media_condition}))
 OR EXISTS(SELECT 1 FROM media_files m JOIN media_retired_keys k USING(storage_key) WHERE m.{media_condition})
 OR EXISTS(SELECT 1 FROM product_images pi JOIN media_files m ON m.id=pi.media_id LEFT JOIN media_legacy_replicas r ON r.media_id=m.id
 WHERE pi.product_id IN({selected}) AND (r.media_id IS NULL OR r.deleted_at IS NOT NULL OR r.storage_key<>m.storage_key
 OR r.sha256<>m.sha256 OR r.byte_size<>m.byte_size OR r.object_key<>'qingye-test/media/'||m.storage_key))
 THEN RAISE EXCEPTION 'source media verification changed'; END IF;""")
    yield "SELECT jsonb_build_object('status','source_verified','package_sha256'," + literal(bundle["sha256"]) + ");\nCOMMIT;"


def iter_import_statements(bundle, administrator, apply=False, database="qingye_production", validation_instance=None):
    if database not in TARGET_DATABASES:
        raise ValueError("unsupported import database")
    _, _, target_port = database_route(database, validation_instance)
    tables = mapped_tables(bundle, administrator)
    package_hash = bundle["sha256"]
    marker = "production-import:" + package_hash
    plan_hash = digest(tables)
    info_sql = literal(encoded({"package_sha256": package_hash, "mapped_sha256": plan_hash, "format": FORMAT})) + "::jsonb"
    imported = f"EXISTS(SELECT 1 FROM admin_logs WHERE id={literal(marker)})"
    guards = [
        f"IF current_database()<>{literal(database)} THEN RAISE EXCEPTION 'wrong target database'; END IF;",
        f"IF current_setting('port')::integer<>{target_port} THEN RAISE EXCEPTION 'wrong target instance'; END IF;",
        f"IF current_setting('max_locks_per_transaction')::bigint*(current_setting('max_connections')::bigint+current_setting('max_prepared_transactions')::bigint)<{len(tables['media_files']) + 2048} THEN RAISE EXCEPTION 'target transaction lock capacity is insufficient'; END IF;",
        "IF EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND backend_type='client backend') THEN RAISE EXCEPTION 'target database has other connected clients'; END IF;",
        f"IF (SELECT jsonb_agg(version ORDER BY version) FROM schema_migrations) IS DISTINCT FROM {literal(encoded(MIGRATIONS))}::jsonb THEN RAISE EXCEPTION 'target migrations differ'; END IF;",
        f"IF ({schema_query()}) IS DISTINCT FROM {literal(encoded(bundle['payload']['schema']))}::jsonb THEN RAISE EXCEPTION 'target schema differs'; END IF;",
        f"IF NOT EXISTS(SELECT 1 FROM users WHERE id={literal(administrator)} AND role='admin' AND enabled AND password_hash ~ '^[$]2[aby][$]12[$]') THEN RAISE EXCEPTION 'independently seeded administrator required'; END IF;",
        f"SELECT {imported} INTO already;",
        f"IF already THEN IF NOT EXISTS(SELECT 1 FROM admin_logs WHERE id={literal(marker)} AND user_id={literal(administrator)} AND action='maintenance.production-import' AND resource_id='initial-production' AND detail={info_sql}) THEN RAISE EXCEPTION 'import marker differs'; END IF;",
    ]
    guards.append("ELSE")
    guards.append("IF (SELECT count(*) FROM users)<>1 OR EXISTS(SELECT 1 FROM users WHERE avatar_media_id IS NOT NULL OR nickname_changed_at IS NOT NULL OR admin_profile_version<>1) THEN RAISE EXCEPTION 'target has existing account data'; END IF;")
    initial = initial_dictionaries()
    empty_tables = set(ALL_TABLES) - {"users", "schema_migrations", "admin_logs", *initial}
    for table in sorted(empty_tables):
        guards.append(f"IF EXISTS(SELECT 1 FROM {table}) THEN RAISE EXCEPTION 'target is not fresh: {table}'; END IF;")
    for table, rows in initial.items():
        guards.append(equal_table_guard(table, literal(encoded(rows)) + "::jsonb"))
    guards.append("""IF (SELECT count(*) FROM admin_logs)<>2 OR EXISTS(SELECT 1 FROM admin_logs WHERE user_id IS NOT NULL OR NOT(
 (id='migration:0001_confirm_legacy_price_units' AND action='maintenance.legacy_price_units' AND resource_id='' AND detail='{"rule":"owner-confirmed-2026-11-11","currency":"CNY","updated_products":0}'::jsonb)
 OR (id='migration:0017_rental_quote_periods' AND action='migration.rental-category' AND resource_id='products' AND detail='{"updated_products":0,"hour_day":0,"week_month":0,"quotes_unchanged":true}'::jsonb))) THEN RAISE EXCEPTION 'target has non-initial audit data'; END IF;""")
    for sequence in ("product_sale_code_seq", "product_rent_code_seq"):
        guards.append(f"IF (SELECT last_value<>1 OR is_called FROM {sequence}) THEN RAISE EXCEPTION 'target sequence was used'; END IF;")
    guards.append("END IF;")
    locks = ""
    if apply:
        # Acquire relation locks before the first SELECT establishes the snapshot.
        locks = "LOCK TABLE " + ",".join(ALL_TABLES) + " IN SHARE ROW EXCLUSIVE MODE;\nSELECT pg_advisory_xact_lock(1834179);\n"
    yield f"""BEGIN TRANSACTION ISOLATION LEVEL SERIALIZABLE {'READ WRITE' if apply else 'READ ONLY'};
SET LOCAL statement_timeout='180s'; SET LOCAL lock_timeout='10s'; SET LOCAL TIME ZONE 'UTC';
SET LOCAL standard_conforming_strings=on;
SET LOCAL search_path=pg_catalog,public;
SET LOCAL work_mem='4MB';
{locks}"""
    yield do_block("\n".join(guards), "already boolean;")
    if apply:
        yield do_block(f"IF NOT {imported} THEN DELETE FROM product_dictionary_options; DELETE FROM product_dictionary_groups; END IF;")
    for table, columns in COLUMNS.items():
        for batch in row_batches(tables[table]):
            actions = [f"IF {'true' if apply else imported} THEN", "expected:=" + literal(encoded(batch)) + "::jsonb;"]
            if apply:
                names = ",".join(columns)
                actions.append(f"IF NOT {imported} THEN INSERT INTO {table}({names}) SELECT {names} FROM jsonb_populate_recordset(NULL::{table},expected); END IF;")
            identity = " AND ".join(f"t.{key}=e.{key}" for key in PRIMARY_KEYS[table])
            actions.append(f"IF EXISTS(WITH expected_rows AS MATERIALIZED(SELECT * FROM jsonb_populate_recordset(NULL::{table},expected)) SELECT * FROM expected_rows EXCEPT SELECT t.* FROM {table} t JOIN expected_rows e ON {identity}) THEN RAISE EXCEPTION 'target table differs: {table}'; END IF; END IF;")
            yield do_block("\n".join(actions), "expected jsonb;")
        # Uniqueness is checked before SQL; equal counts plus every expected row
        # present proves complete equality, including no unexpected target rows.
        yield do_block(f"IF {'true' if apply else imported} THEN IF (SELECT count(*) FROM {table})<>{len(tables[table])} THEN RAISE EXCEPTION 'target row count differs: {table}'; END IF; END IF;")
    if apply:
        finish = [f"IF NOT {imported} THEN", f"INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES({literal(marker)},{literal(administrator)},'maintenance.production-import','initial-production',{info_sql});"]
        for sequence, prefix in (("product_sale_code_seq", "QYCH"), ("product_rent_code_seq", "QYZH")):
            values = [int(row["product_code"][4:]) for row in tables["products"] if re.fullmatch(prefix + r"[0-9]{1,15}", row["product_code"])]
            # RESTART is transactional; unlike setval, rollback cannot leave used sequences.
            finish.append(f"ALTER SEQUENCE {sequence} RESTART WITH {max(values, default=0) + 1};")
        finish.append("END IF;")
        yield do_block("\n".join(finish))
    yield f"""SELECT jsonb_build_object('status',CASE WHEN {imported} THEN 'verified_import' ELSE 'ready_to_import' END,
 'package_sha256',{literal(package_hash)},'mapped_sha256',{literal(plan_hash)},'applied',{str(apply).lower()});
COMMIT;"""


def run_psql(database, statement, validation_instance=None):
    if database not in {"qingye_test", *TARGET_DATABASES}:
        raise ValueError("unsupported local database")
    binary, socket, port = database_route(database, validation_instance)
    command = ["runuser", "-u", "postgres", "--", binary, "-X", "-qAt", "-h", socket, "-p", str(port), "-U", "postgres", "-v", "ON_ERROR_STOP=1", "-v", "VERBOSITY=sqlstate", "-d", database]
    environment = {key: value for key, value in os.environ.items() if not key.startswith("PG")}
    environment["PGCLIENTENCODING"] = "UTF8"
    known = {}
    # TemporaryFile is unlinked/private on Linux; neither the SQL nor credentials
    # enter arguments, logs or a persistent file. Each statement is bounded.
    with tempfile.TemporaryFile(mode="w+", encoding="utf-8") as source:
        for part in [statement] if isinstance(statement, str) else statement:
            known.update({code: message for message, code in re.findall(r"RAISE EXCEPTION '([^']+)' USING ERRCODE='([^']+)'", part)})
            source.write(part)
            source.write("\n")
        source.seek(0)
        result = subprocess.run(command, stdin=source, text=True, capture_output=True, cwd="/tmp", timeout=240, env=environment)
    if result.returncode != 0:
        # SQL errors can contain failing business rows. Never expose stderr or SQL.
        matched = re.search(r"(?:ERROR|FATAL):\s+([0-9A-Z]{5})\b", result.stderr)
        state = matched[1] if matched else None
        raise DatabaseOperationError(state, known.get(state))
    lines = [line for line in result.stdout.splitlines() if line.startswith("{")]
    if len(lines) != 1:
        raise RuntimeError("unexpected database response")
    return decoded(lines[0])


def ensure_private(path):
    if path.is_symlink() or not path.is_file() or path.stat().st_size > MAX_PACKAGE_BYTES:
        raise ValueError("private regular file required within package size limit")
    if os.name == "posix" and path.stat().st_mode & 0o077:
        raise ValueError("private file must not be group/world accessible")


def load_bundle(path):
    ensure_private(path)
    return validate_bundle(decoded(path.read_text(encoding="utf-8")))


def environment_values(path):
    ensure_private(path)
    values = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if line and not line.startswith("#") and "=" in line:
            key, value = line.split("=", 1)
            values[key] = value.strip().strip('"\'')
    return values


def credential_fingerprint(values):
    try:
        key = base64.b64decode(values.get("PRODUCT_CREDENTIAL_KEY", ""), validate=True)
    except ValueError:
        raise ValueError("invalid credential key configuration") from None
    if len(key) != 32:
        raise ValueError("private credentials require a 32-byte product key")
    return hashlib.sha256(key).hexdigest()


def check_target_environment(path, bundle, target_database="qingye_production", validation_instance=None):
    values = environment_values(path)
    database = urlsplit(values.get("DATABASE_URL", ""))
    _, _, target_port = database_route(target_database, validation_instance)
    if target_database not in TARGET_DATABASES or values.get("APP_ENV") != "production" or values.get("MEDIA_WORKERS_ENABLED", "").lower() != "false" or database.path != "/" + target_database or database.hostname not in {"127.0.0.1", "localhost", "::1"} or (database.port or 5432) != target_port:
        raise ValueError("production environment must target the selected local database with media workers explicitly disabled")
    if values.get("SMS_REGISTER_ENABLED", "false").lower() != "false" or values.get("SMS_LOGIN_ENABLED", "false").lower() != "false":
        raise ValueError("SMS must stay disabled during initial import")
    if bundle["payload"]["credential_key_sha256"] is not None and credential_fingerprint(values) != bundle["payload"]["credential_key_sha256"]:
        raise ValueError("product credential encryption key does not match source ciphertext")


def write_private(path, value):
    if not path.is_absolute() or path.exists() or path.is_symlink():
        raise ValueError("new absolute private output path required")
    repository = Path(__file__).resolve().parents[2]
    if (repository / ".git").exists() and (path.resolve() == repository or repository in path.resolve().parents):
        raise ValueError("business packages must be outside the repository")
    if not path.parent.exists() or path.parent.is_symlink():
        raise ValueError("prepare a private output directory first")
    if os.name == "posix" and path.parent.stat().st_mode & 0o077:
        raise ValueError("output directory must be private")
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as handle:
        for chunk in encoded_chunks(value):
            handle.write(chunk)
        handle.write("\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    export = commands.add_parser("export")
    export.add_argument("--output", type=Path, required=True)
    export.add_argument("--image-order-manifest", type=Path, required=True)
    export.add_argument("--source-media-dir", type=Path, required=True)
    export.add_argument("--source-environment-file", type=Path, help="required only when selected products have encrypted private credentials")
    inspect = commands.add_parser("inspect")
    inspect.add_argument("--package", type=Path, required=True)
    verify = commands.add_parser("verify-source")
    verify.add_argument("--package", type=Path, required=True)
    verify.add_argument("--image-order-manifest", type=Path, required=True)
    verify.add_argument("--source-media-dir", type=Path, required=True)
    imp = commands.add_parser("import")
    imp.add_argument("--package", type=Path, required=True)
    imp.add_argument("--database", choices=sorted(TARGET_DATABASES), default="qingye_production")
    imp.add_argument("--validation-instance", choices=["test", "production"], help="required only for the dedicated validation database")
    imp.add_argument("--administrator-id", required=True)
    imp.add_argument("--environment-file", type=Path, required=True)
    imp.add_argument("--expected-sha256", required=True)
    imp.add_argument("--image-order-manifest", type=Path, required=True)
    imp.add_argument("--apply", action="store_true", help="default is a read-only target preview")
    args = parser.parse_args()
    if args.command == "export":
        source = run_psql("qingye_test", export_statement())
        verify_support_files(source, args.source_media_dir)
        fingerprint = None
        if source["tables"]["product_private_credentials"]:
            if not args.source_environment_file:
                raise ValueError("source environment file required for encrypted private credentials")
            fingerprint = credential_fingerprint(environment_values(args.source_environment_file))
        bundle = prepare_bundle(source, credential_key_sha256=fingerprint)
        bundle = bind_image_order(bundle, args.image_order_manifest)
        write_private(args.output, bundle)
        output = summary(bundle)
    else:
        bundle = load_bundle(args.package)
        if args.command == "inspect":
            output = summary(bundle)
        elif args.command == "verify-source":
            if bind_image_order(bundle, args.image_order_manifest)["sha256"] != bundle["sha256"]:
                raise ValueError("package is not bound to the verified image order")
            verify_support_files(bundle["payload"], args.source_media_dir)
            output = run_psql("qingye_test", iter_source_verification_statements(bundle))
        else:
            if args.expected_sha256 != bundle["sha256"]:
                raise ValueError("explicit package checksum does not match")
            if bundle["payload"]["image_order_sha256"] is None or bind_image_order(bundle, args.image_order_manifest)["sha256"] != bundle["sha256"]:
                raise ValueError("package is not bound to the verified image order")
            check_target_environment(args.environment_file, bundle, args.database, args.validation_instance)
            output = run_psql(args.database, iter_import_statements(bundle, args.administrator_id, args.apply, args.database, args.validation_instance), args.validation_instance)
    print(encoded(output))


if __name__ == "__main__":
    try:
        main()
    except DatabaseOperationError as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1)
    except (ValueError, KeyError, TypeError, OSError, RuntimeError, subprocess.SubprocessError):
        # Inputs may contain private data or credentials; diagnostics are deliberately structural.
        print("Production data preparation rejected; verify the private package, target baseline and configuration.", file=sys.stderr)
        raise SystemExit(1)
