import json
import os
import re
from pathlib import Path
from typing import Any, Literal

import httpx
from fastapi import FastAPI, HTTPException
from json_repair import repair_json
from pydantic import BaseModel, Field, field_validator
from dotenv import load_dotenv


# Always resolve the AI service configuration relative to this service instead
# of depending on the shell's current working directory.
load_dotenv(Path(__file__).resolve().parents[1] / ".env")


class StructureRequest(BaseModel):
    content: str = Field(min_length=1, max_length=2_000_000)


class StructuredItem(BaseModel):
    item_index: int = Field(ge=1)
    raw_text: str = Field(min_length=1)
    detected_type: Literal["buy_demand", "sell_project", "unknown"]
    classification_confidence: float = Field(ge=0, le=1)
    title: str = ""
    structured_data: dict[str, Any] = Field(default_factory=dict)

    @field_validator("raw_text", "title")
    @classmethod
    def trim(cls, value: str) -> str:
        return value.strip()


class StructureResponse(BaseModel):
    items: list[StructuredItem]
    model_name: str


class CandidateSegment(BaseModel):
    segment_id: int = Field(ge=1)
    raw_text: str = Field(min_length=1)
    start_offset: int = Field(ge=0)
    end_offset: int = Field(gt=0)
    block_index: int = Field(default=1, ge=1)


class SegmentResponse(BaseModel):
    segments: list[CandidateSegment]


class AtomicGroup(BaseModel):
    group_index: int = Field(ge=1)
    segment_ids: list[int] = Field(min_length=1)
    reason: str = ""


class GroupRequest(BaseModel):
    segments: list[CandidateSegment] = Field(min_length=1, max_length=500)


class GroupResponse(BaseModel):
    groups: list[AtomicGroup]
    model_name: str


class AtomicItemInput(BaseModel):
    atomic_id: int = Field(ge=1)
    segment_ids: list[int] = Field(min_length=1)
    raw_text: str = Field(min_length=1)


class StructureGroupsRequest(BaseModel):
    items: list[AtomicItemInput] = Field(min_length=1, max_length=500)


class EmbeddingRequest(BaseModel):
    texts: list[str] = Field(min_length=1, max_length=64)

    @field_validator("texts")
    @classmethod
    def validate_texts(cls, values: list[str]) -> list[str]:
        result = [value.strip() for value in values]
        if any(not value for value in result):
            raise ValueError("embedding texts must not be empty")
        if any(len(value) > 100_000 for value in result):
            raise ValueError("embedding text is too long")
        return result


class EmbeddingResponse(BaseModel):
    vectors: list[list[float]]
    model_name: str
    dimensions: int


app = FastAPI(title="MatchMind AI Service", version="0.1.0")


SYSTEM_PROMPT = """你是投融资交易信息结构化引擎。输入可能包含一条或多条独立信息。

任务顺序：
1. 按编号、段落、交易主体和语义完整性拆分独立信息项。不要把同一条需求里的子条件拆开。
   - 连续编号分别描述不同行业、不同主体、不同项目或各自财务指标时，每个编号必须输出为独立items元素。
   - 连续编号只是同一主体的一组条件（如收购方向、地区、营收、利润、医院属性）时，才保留为同一项。
   - 禁止生成“多个项目”“多个需求”“若干标的”等汇总项。
   - 禁止使用extra_facts.sub_projects或extra_constraints.sub_requirements保存多个可独立交易的信息，必须提升为多个items元素。
2. 判断每项方向：
   - buy_demand：买方、资金方、基金、上市公司正在寻找项目、资产、公司、订单或控制权。
   - sell_project：项目、公司、资产正在融资、出售、转让或寻求被并购。
   - unknown：无法可靠判断。
3. 提取结构化字段。所有金额统一换算成人民币“元”；百分比使用0到1的小数；无法确认的字段用null。不得猜测原文没有的信息。

必须返回单个JSON对象：{\"items\":[...]}。每项必须包含：
item_index, raw_text, detected_type, classification_confidence, title, structured_data。

buy_demand 的 structured_data 可包含：
title,buyer_name,buyer_type,summary,industries,transaction_types,target_types,preferred_regions,preferred_stages,
investment_amount_min,investment_amount_max,target_revenue_min,target_revenue_max,target_net_profit_min,target_net_profit_max,
target_valuation_min,target_valuation_max,pe_min,pe_max,listed_status_requirement,control_ratio_min,
profitability_required,extra_constraints。

sell_project 的 structured_data 可包含：
title,company_name,summary,industries,transaction_types,project_types,financing_round,
financing_amount_min,financing_amount_max,revenue_min,revenue_max,net_profit_min,net_profit_max,
valuation_min,valuation_max,pe_min,pe_max,province,city,listed_status,transfer_ratio_min,transfer_ratio_max,extra_facts。

industries、transaction_types、target_types、project_types、preferred_regions、preferred_stages 必须是JSON数组。
extra_constraints、extra_facts 必须是JSON对象。

特别注意：“上市公司拟收购某项目”是buy_demand；“某项目寻求上市公司收购”是sell_project。不能仅根据“收购”二字分类。
判断方向必须看交易动作的宾语，而不是只看句子主语：“某项目/某企业寻求收购、并购、借壳某上市公司控制权”表示该项目方是收购方，必须归为buy_demand；只有“某项目寻求被上市公司收购/并购”才归为sell_project。"""


GROUPING_PROMPT = """你是投融资信息原子边界判断器。输入是本地候选分段JSON，每段包含segment_id和不可修改的raw_text。

你的唯一任务是把连续候选段组合成可独立录入和匹配的原子信息组：
- 同一买方或卖方的一组条件（方向、行业、地区、营收、利润、估值、比例等）必须归为同一组。
- 不同主体、不同项目、不同交易机会或分别拥有财务指标的连续编号必须分为不同组。
- 无法判断的信息也必须归入某一组，不能遗漏。
- 每个segment_id必须且只能出现一次；只能引用输入中的ID；不得改变顺序；每组ID必须连续。
- 不得提取字段、不得改写原文、不得创造新信息。

只返回严格JSON：{"groups":[{"group_index":1,"segment_ids":[1,2],"reason":"同一收购需求的条件"}]}。"""


ATOMIC_STRUCTURE_PROMPT = """你是投融资交易信息结构化引擎。输入是已经完成原子分组的JSON数组。
每项包含atomic_id、segment_ids和raw_text。分组边界已经确定，禁止合并、拆分、遗漏或重排。

对每项判断方向：
- buy_demand：买方、资金方、基金、上市公司正在寻找项目、资产、公司、订单或控制权。
- sell_project：项目、公司、资产正在融资、出售、转让或寻求被并购。
- unknown：无法可靠判断。

提取结构化字段。金额统一为人民币元，百分比使用0到1小数，无法确认用null，不得猜测。
禁止汇总标题，禁止extra_facts.sub_projects和extra_constraints.sub_requirements。

必须返回严格JSON：{"items":[...]}。每项必须包含：
atomic_id, detected_type, classification_confidence, title, structured_data。

buy_demand字段：title,buyer_name,buyer_type,summary,industries,transaction_types,target_types,preferred_regions,
preferred_stages,investment_amount_min,investment_amount_max,target_revenue_min,target_revenue_max,
target_net_profit_min,target_net_profit_max,target_valuation_min,target_valuation_max,pe_min,pe_max,
listed_status_requirement,control_ratio_min,profitability_required,extra_constraints。

sell_project字段：title,company_name,summary,industries,transaction_types,project_types,financing_round,
financing_amount_min,financing_amount_max,revenue_min,revenue_max,net_profit_min,net_profit_max,
valuation_min,valuation_max,pe_min,pe_max,province,city,listed_status,transfer_ratio_min,transfer_ratio_max,extra_facts。

数组字段必须是JSON数组，extra_constraints和extra_facts必须是JSON对象。
判断方向必须看交易动作的宾语：“某项目/某企业寻求收购、并购、借壳某上市公司控制权”是buy_demand；“某项目寻求被上市公司收购/并购”才是sell_project。"""


SYSTEM_PROMPT += """
补充业务边界：
- “控转”是“控制权转让”的简称。公司控转、上市公司控转、转让控制权均表示标的控制权对外出售，归为sell_project。
- 本系统只收录股权投资、融资、并购、资产或控制权交易。矿石等普通商品贸易中的收货、求购、采购不是投资需求，归为unknown。"""

GROUPING_PROMPT += """
- 标题后接正文时，标题必须与其后的正文成组，不能并入上一个项目。例如“矿石贸易”应与下一行“收国内矿石”成组。
- 带（一）（二）（三）或类似序号的同系列项目，默认是不同交易机会，必须分组。"""

ATOMIC_STRUCTURE_PROMPT += """
补充业务边界：“控转”是控制权转让，归为sell_project；普通商品或矿石贸易采购不是投资需求，归为unknown。"""


@app.get("/health")
async def health() -> dict[str, str]:
    configured = bool(os.getenv("EMBEDDING_API_KEY", "").strip() and os.getenv("EMBEDDING_MODEL", "").strip())
    return {
        "status": "ok",
        "embedding": "configured" if configured else "not_configured",
        "embedding_model": os.getenv("EMBEDDING_MODEL", "").strip(),
        "structure_model": os.getenv("AI_MODEL", "").strip(),
    }


@app.post("/v1/embeddings", response_model=EmbeddingResponse)
async def embeddings(request: EmbeddingRequest) -> EmbeddingResponse:
    api_key = os.getenv("EMBEDDING_API_KEY", "").strip()
    model = os.getenv("EMBEDDING_MODEL", "").strip()
    if not api_key or not model:
        raise HTTPException(status_code=503, detail="embedding service is not configured")

    api_base = os.getenv("EMBEDDING_API_BASE", "https://dashscope.aliyuncs.com/compatible-mode/v1").rstrip("/")
    dimensions = int(os.getenv("EMBEDDING_DIMENSIONS", "1024"))
    send_dimensions = os.getenv("EMBEDDING_SEND_DIMENSIONS", "false").strip().lower() == "true"
    timeout = float(os.getenv("EMBEDDING_TIMEOUT_SECONDS", "60"))
    payload: dict[str, Any] = {"model": model, "input": request.texts, "encoding_format": "float"}
    if send_dimensions and dimensions:
        payload["dimensions"] = dimensions
    try:
        async with httpx.AsyncClient(timeout=timeout) as client:
            response = await client.post(
                f"{api_base}/embeddings",
                headers={"Authorization": f"Bearer {api_key}"},
                json=payload,
            )
            response.raise_for_status()
            data = sorted(response.json()["data"], key=lambda item: item["index"])
        vectors = [item["embedding"] for item in data]
        if len(vectors) != len(request.texts):
            raise ValueError("embedding response count mismatch")
        if any(len(vector) != dimensions for vector in vectors):
            raise ValueError("embedding dimension mismatch")
        return EmbeddingResponse(vectors=vectors, model_name=model, dimensions=dimensions)
    except (httpx.HTTPError, KeyError, TypeError, ValueError) as exc:
        raise HTTPException(status_code=502, detail=f"embedding failed: {exc}") from exc


@app.post("/v1/structure", response_model=StructureResponse)
async def structure(request: StructureRequest) -> StructureResponse:
    api_key = os.getenv("AI_API_KEY", "").strip()
    model = os.getenv("AI_MODEL", "").strip()
    if not api_key or not model:
        return StructureResponse(items=fallback_structure(request.content), model_name="local-fallback-v1")

    api_base = os.getenv("AI_API_BASE", "https://api.openai.com/v1").rstrip("/")
    timeout = float(os.getenv("AI_TIMEOUT_SECONDS", "90"))
    try:
        candidate_list = candidate_segments(request.content)
        async with httpx.AsyncClient(timeout=timeout) as client:
            groups = await group_segments_with_ai(
                client, api_base, api_key, model, candidate_list
            )
            atomic_inputs = build_atomic_inputs(request.content, candidate_list, groups)
            items = await structure_atomic_groups_with_ai(
                client, api_base, api_key, model, atomic_inputs
            )
        validate_atomic_items(atomic_inputs, items)
        return StructureResponse(items=items, model_name=model)
    except (httpx.HTTPError, KeyError, TypeError, ValueError) as exc:
        raise HTTPException(status_code=502, detail=f"AI structure failed: {exc}") from exc


@app.post("/v1/segments", response_model=SegmentResponse)
async def segments(request: StructureRequest) -> SegmentResponse:
    return SegmentResponse(segments=candidate_segments(request.content))


@app.post("/v1/atomic-groups", response_model=GroupResponse)
async def atomic_groups(request: GroupRequest) -> GroupResponse:
    api_key, model, api_base, timeout = ai_settings()
    try:
        validate_candidate_segments(request.segments)
        async with httpx.AsyncClient(timeout=timeout) as client:
            groups = await group_segments_with_ai(
                client, api_base, api_key, model, request.segments
            )
        return GroupResponse(groups=groups, model_name=model)
    except (httpx.HTTPError, KeyError, TypeError, ValueError) as exc:
        raise HTTPException(status_code=502, detail=f"AI grouping failed: {exc}") from exc


@app.post("/v1/structure-groups", response_model=StructureResponse)
async def structure_groups(request: StructureGroupsRequest) -> StructureResponse:
    api_key, model, api_base, timeout = ai_settings()
    try:
        validate_atomic_inputs(request.items)
        async with httpx.AsyncClient(timeout=timeout) as client:
            items = await structure_atomic_groups_with_ai(
                client, api_base, api_key, model, request.items
            )
        validate_atomic_items(request.items, items)
        return StructureResponse(items=items, model_name=model)
    except (httpx.HTTPError, KeyError, TypeError, ValueError) as exc:
        raise HTTPException(status_code=502, detail=f"AI atomic structure failed: {exc}") from exc


def ai_settings() -> tuple[str, str, str, float]:
    api_key = os.getenv("AI_API_KEY", "").strip()
    model = os.getenv("AI_MODEL", "").strip()
    if not api_key or not model:
        raise ValueError("AI service is not configured")
    api_base = os.getenv("AI_API_BASE", "https://api.openai.com/v1").rstrip("/")
    return api_key, model, api_base, float(os.getenv("AI_TIMEOUT_SECONDS", "90"))


async def call_json_model(
    client: httpx.AsyncClient,
    api_base: str,
    api_key: str,
    model: str,
    system_prompt: str,
    user_payload: Any,
) -> dict[str, Any]:
    response = await client.post(
        f"{api_base}/chat/completions",
        headers={"Authorization": f"Bearer {api_key}"},
        json={
            "model": model,
            "thinking": {"type": "disabled"},
            "temperature": 0,
            "max_tokens": 8192,
            "response_format": {"type": "json_object"},
            "messages": [
                {"role": "system", "content": system_prompt},
                {"role": "user", "content": json.dumps(user_payload, ensure_ascii=False)},
            ],
        },
    )
    response.raise_for_status()
    return parse_model_json(response.json()["choices"][0]["message"]["content"])


async def group_segments_with_ai(
    client: httpx.AsyncClient,
    api_base: str,
    api_key: str,
    model: str,
    candidate_list: list[CandidateSegment],
) -> list[AtomicGroup]:
    validate_candidate_segments(candidate_list)
    groups: list[AtomicGroup] = []
    for chunk_number, chunk in enumerate(grouping_chunks(candidate_list), 1):
        parsed = await call_json_model(
            client, api_base, api_key, model, GROUPING_PROMPT,
            {"chunk_number": chunk_number, "segments": [segment.model_dump() for segment in chunk]},
        )
        raw_groups = parsed.get("groups")
        if not isinstance(raw_groups, list):
            raise ValueError(f"grouping model returned invalid groups for chunk {chunk_number}")
        chunk_groups = [AtomicGroup.model_validate(group) for group in raw_groups]
        validate_group_coverage(chunk, chunk_groups)
        for group in chunk_groups:
            groups.append(group.model_copy(update={"group_index": len(groups) + 1}))
    validate_group_coverage(candidate_list, groups)
    return groups


async def structure_atomic_groups_with_ai(
    client: httpx.AsyncClient,
    api_base: str,
    api_key: str,
    model: str,
    atomic_inputs: list[AtomicItemInput],
) -> list[StructuredItem]:
    validate_atomic_inputs(atomic_inputs)
    by_id: dict[int, dict[str, Any]] = {}
    for chunk_number, chunk in enumerate(structure_chunks(atomic_inputs), 1):
        parsed = await call_json_model(
            client, api_base, api_key, model, ATOMIC_STRUCTURE_PROMPT,
            {"chunk_number": chunk_number, "items": [item.model_dump() for item in chunk]},
        )
        raw_items = parsed.get("items")
        if not isinstance(raw_items, list):
            raise ValueError(f"structure model returned invalid items for chunk {chunk_number}")
        chunk_ids: set[int] = set()
        for raw_item in raw_items:
            if not isinstance(raw_item, dict):
                raise ValueError("structure model returned a non-object item")
            atomic_id = raw_item.get("atomic_id")
            if not isinstance(atomic_id, int) or atomic_id in by_id:
                raise ValueError("structure model returned an invalid or duplicate atomic_id")
            by_id[atomic_id] = raw_item
            chunk_ids.add(atomic_id)
        expected_chunk_ids = {item.atomic_id for item in chunk}
        if chunk_ids != expected_chunk_ids:
            missing = sorted(expected_chunk_ids - chunk_ids)
            unexpected = sorted(chunk_ids - expected_chunk_ids)
            raise ValueError(
                f"structure chunk {chunk_number} coverage mismatch: missing={missing}, unexpected={unexpected}"
            )
    expected_ids = [item.atomic_id for item in atomic_inputs]
    if set(by_id) != set(expected_ids) or len(by_id) != len(expected_ids):
        raise ValueError("structure model did not return exactly one item for every atomic group")
    result: list[StructuredItem] = []
    for index, atomic_input in enumerate(atomic_inputs, 1):
        raw_item = dict(by_id[atomic_input.atomic_id])
        raw_item["item_index"] = index
        raw_item["raw_text"] = atomic_input.raw_text
        result.append(StructuredItem.model_validate(normalize_structured_item(raw_item, index)))
    return result


def strip_code_fence(value: str) -> str:
    value = value.strip()
    if value.startswith("```"):
        value = re.sub(r"^```(?:json)?\s*", "", value)
        value = re.sub(r"\s*```$", "", value)
    return value


def parse_model_json(content: str) -> dict[str, Any]:
    cleaned = strip_code_fence(content)
    try:
        parsed = json.loads(cleaned)
    except json.JSONDecodeError as original_error:
        try:
            parsed = repair_json(cleaned, return_objects=True)
        except Exception as repair_error:
            raise ValueError(
                f"model returned malformed JSON; parse={original_error}; repair={repair_error}"
            ) from repair_error
    if not isinstance(parsed, dict):
        raise ValueError("model returned a non-object JSON value")
    return parsed


def normalize_structured_item(item: Any, index: int, fallback_raw_text: str = "") -> dict[str, Any]:
    if not isinstance(item, dict):
        raise ValueError(f"item {index} is not an object")
    result = dict(item)
    structured_data = result.get("structured_data")
    if not isinstance(structured_data, dict):
        structured_data = {}
    raw_text = result.get("raw_text")
    if not isinstance(raw_text, str) or not raw_text.strip():
        raw_text = fallback_raw_text
    raw_text = raw_text.strip()
    if not raw_text:
        raise ValueError(f"item {index} has no raw_text")
    title = result.get("title")
    if not isinstance(title, str) or not title.strip():
        structured_title = structured_data.get("title")
        title = structured_title if isinstance(structured_title, str) else ""
    if not title.strip():
        title = make_title(raw_text)
    title = preserve_series_marker(title.strip(), raw_text)
    detected_type = result.get("detected_type")
    if detected_type not in {"buy_demand", "sell_project", "unknown"}:
        detected_type = "unknown"
    if acquisition_buyer_override(raw_text):
        detected_type = "buy_demand"
        structured_data = sell_fields_to_buy_fields(structured_data)
    elif control_transfer_override(raw_text):
        detected_type = "sell_project"
        structured_data = buy_fields_to_sell_fields(structured_data)
    elif commodity_trade_override(raw_text):
        detected_type = "unknown"
        structured_data = {"title": title.strip(), "summary": raw_text}
    try:
        confidence = float(result.get("classification_confidence"))
    except (TypeError, ValueError):
        confidence = 0.5
    result.update({
        "item_index": result.get("item_index") if isinstance(result.get("item_index"), int) and result["item_index"] > 0 else index,
        "raw_text": raw_text,
        "title": title,
        "detected_type": detected_type,
        "classification_confidence": min(1.0, max(0.0, confidence)),
        "structured_data": structured_data,
    })
    return result


def preserve_series_marker(title: str, raw_text: str) -> str:
    match = re.search(r"[（(]([一二三四五六七八九十\d]+)[）)]", raw_text)
    if not match:
        return title
    marker = f"（{match.group(1)}）"
    if marker in title or f"({match.group(1)})" in title:
        return title
    return f"{title}{marker}"


def acquisition_buyer_override(text: str) -> bool:
    """Identify a project/company acting as acquirer, not seeking to be acquired."""
    normalized = re.sub(r"\s+", "", text)
    if re.search(r"寻求被.{0,12}(?:收购|并购)", normalized):
        return False
    return bool(re.search(
        r"(?:项目|企业|公司).{0,18}(?:寻求|拟|计划|意向).{0,10}"
        r"(?:借壳(?:收购)?|收购|并购).{0,12}(?:上市公司|A股公司).{0,8}(?:控制权|控股权|股份|股票)",
        normalized,
    ))


def sell_fields_to_buy_fields(data: dict[str, Any]) -> dict[str, Any]:
    """Convert fields emitted under the wrong sell schema into the buy schema."""
    result = dict(data)
    aliases = {
        "financing_amount_min": "investment_amount_min",
        "financing_amount_max": "investment_amount_max",
        "revenue_min": "target_revenue_min",
        "revenue_max": "target_revenue_max",
        "net_profit_min": "target_net_profit_min",
        "net_profit_max": "target_net_profit_max",
        "valuation_min": "target_valuation_min",
        "valuation_max": "target_valuation_max",
        "listed_status": "listed_status_requirement",
        "transfer_ratio_min": "control_ratio_min",
    }
    for source, target in aliases.items():
        value = result.pop(source, None)
        if value is not None and target not in result:
            result[target] = value
    result.pop("project_types", None)
    result.pop("transfer_ratio_max", None)
    result.pop("financing_round", None)
    result.pop("province", None)
    result.pop("city", None)
    facts = result.pop("extra_facts", None)
    if isinstance(facts, dict):
        constraints = result.get("extra_constraints")
        result["extra_constraints"] = {
            **facts,
            **(constraints if isinstance(constraints, dict) else {}),
        }
    result["target_types"] = ["上市公司"]
    result.setdefault("preferred_regions", [])
    result.setdefault("preferred_stages", [])
    result.setdefault("extra_constraints", {})
    return result


def control_transfer_override(text: str) -> bool:
    normalized = re.sub(r"\s+", "", text)
    return bool(re.search(r"(?:控转|控制权转让|转让控制权|控股权转让)", normalized))


def commodity_trade_override(text: str) -> bool:
    normalized = re.sub(r"\s+", "", text)
    trade_goods = r"(?:矿石|铜石|重晶石|铅锌石|煤炭|钢材|粮食|原料|货物|商品)"
    trade_actions = r"(?:贸易|采购|求购|收购?|买入)"
    return bool(re.search(trade_goods, normalized) and re.search(trade_actions, normalized))


def buy_fields_to_sell_fields(data: dict[str, Any]) -> dict[str, Any]:
    result = dict(data)
    aliases = {
        "target_types": "project_types",
        "investment_amount_min": "financing_amount_min",
        "investment_amount_max": "financing_amount_max",
        "target_revenue_min": "revenue_min",
        "target_revenue_max": "revenue_max",
        "target_net_profit_min": "net_profit_min",
        "target_net_profit_max": "net_profit_max",
        "target_valuation_min": "valuation_min",
        "target_valuation_max": "valuation_max",
        "listed_status_requirement": "listed_status",
        "control_ratio_min": "transfer_ratio_min",
    }
    for source, target in aliases.items():
        value = result.pop(source, None)
        if value is not None and target not in result:
            result[target] = value
    constraints = result.pop("extra_constraints", None)
    if isinstance(constraints, dict):
        facts = result.get("extra_facts")
        result["extra_facts"] = {**constraints, **(facts if isinstance(facts, dict) else {})}
    for key in ("buyer_name", "buyer_type", "preferred_regions", "preferred_stages", "profitability_required"):
        result.pop(key, None)
    result.setdefault("project_types", ["上市公司"])
    result.setdefault("extra_facts", {})
    return result


def candidate_segments(content: str) -> list[CandidateSegment]:
    if not content.strip():
        raise ValueError("content is empty")
    boundaries = {0, len(content)}
    block_breaks: list[int] = []
    for match in re.finditer(r"\r?\n[ \t\u00a0]*\r?\n+", content):
        boundaries.add(match.end())
        block_breaks.append(match.end())
    marker = re.compile(
        r"(?m)(?<!\S)"
        r"(?=(?:\d{1,2}(?:、|\.(?!\d))|[一二三四五六七八九十]{1,3}、)"
        r"[ \t\u00a0]*\S)"
    )
    for match in marker.finditer(content):
        boundaries.add(match.start())
    ordered = sorted(boundaries)
    result: list[CandidateSegment] = []
    for start, end in zip(ordered, ordered[1:]):
        while start < end and content[start].isspace():
            start += 1
        while end > start and content[end - 1].isspace():
            end -= 1
        if start >= end:
            continue
        result.append(CandidateSegment(
            segment_id=len(result) + 1,
            raw_text=content[start:end],
            start_offset=start,
            end_offset=end,
            block_index=1 + sum(block <= start for block in block_breaks),
        ))
    validate_candidate_segments(result, content)
    return result


def grouping_chunks(
    candidate_list: list[CandidateSegment], max_segments: int = 12
) -> list[list[CandidateSegment]]:
    if max_segments < 1:
        raise ValueError("max_segments must be positive")
    blocks: list[list[CandidateSegment]] = []
    for segment in candidate_list:
        if not blocks or blocks[-1][-1].block_index != segment.block_index:
            blocks.append([])
        blocks[-1].append(segment)
    chunks: list[list[CandidateSegment]] = []
    current: list[CandidateSegment] = []
    for block in blocks:
        if current and len(current) + len(block) > max_segments:
            chunks.append(current)
            current = []
        # Never split a local paragraph block, even if it exceeds the soft limit.
        current.extend(block)
        if len(current) >= max_segments:
            chunks.append(current)
            current = []
    if current:
        chunks.append(current)
    return chunks


def structure_chunks(
    items: list[AtomicItemInput], max_items: int = 10
) -> list[list[AtomicItemInput]]:
    if max_items < 1:
        raise ValueError("max_items must be positive")
    return [items[index:index + max_items] for index in range(0, len(items), max_items)]


def validate_candidate_segments(
    candidate_list: list[CandidateSegment], content: str | None = None
) -> None:
    if not candidate_list:
        raise ValueError("candidate segmentation returned no segments")
    expected_ids = list(range(1, len(candidate_list) + 1))
    if [segment.segment_id for segment in candidate_list] != expected_ids:
        raise ValueError("candidate segment_ids must be consecutive and ordered")
    previous_end = -1
    for segment in candidate_list:
        if segment.start_offset < previous_end or segment.end_offset <= segment.start_offset:
            raise ValueError("candidate segment offsets overlap or are invalid")
        if content is not None:
            if segment.end_offset > len(content):
                raise ValueError("candidate segment offset is outside source content")
            if content[segment.start_offset:segment.end_offset] != segment.raw_text:
                raise ValueError("candidate segment text does not match source offsets")
        previous_end = segment.end_offset


def validate_group_coverage(
    candidate_list: list[CandidateSegment], groups: list[AtomicGroup]
) -> None:
    if not groups:
        raise ValueError("grouping model returned no groups")
    if [group.group_index for group in groups] != list(range(1, len(groups) + 1)):
        raise ValueError("group indexes must be consecutive and ordered")
    expected = [segment.segment_id for segment in candidate_list]
    flattened: list[int] = []
    for group in groups:
        if group.segment_ids != sorted(group.segment_ids):
            raise ValueError(f"group {group.group_index} segment_ids are out of order")
        if group.segment_ids != list(range(group.segment_ids[0], group.segment_ids[-1] + 1)):
            raise ValueError(f"group {group.group_index} contains non-contiguous segments")
        flattened.extend(group.segment_ids)
    if flattened != expected:
        missing = sorted(set(expected) - set(flattened))
        duplicates = sorted({value for value in flattened if flattened.count(value) > 1})
        unexpected = sorted(set(flattened) - set(expected))
        raise ValueError(
            f"segment coverage mismatch: missing={missing}, duplicates={duplicates}, unexpected={unexpected}"
        )


def build_atomic_inputs(
    content: str,
    candidate_list: list[CandidateSegment],
    groups: list[AtomicGroup],
) -> list[AtomicItemInput]:
    validate_candidate_segments(candidate_list, content)
    validate_group_coverage(candidate_list, groups)
    by_id = {segment.segment_id: segment for segment in candidate_list}
    result: list[AtomicItemInput] = []
    for group in groups:
        selected = [by_id[segment_id] for segment_id in group.segment_ids]
        start, end = selected[0].start_offset, selected[-1].end_offset
        raw_text = content[start:end].strip()
        if not raw_text:
            raise ValueError(f"atomic group {group.group_index} has no source text")
        result.append(AtomicItemInput(
            atomic_id=group.group_index,
            segment_ids=group.segment_ids,
            raw_text=raw_text,
        ))
    validate_atomic_inputs(result)
    return result


def validate_atomic_inputs(items: list[AtomicItemInput]) -> None:
    if not items:
        raise ValueError("there are no atomic groups to structure")
    if [item.atomic_id for item in items] != list(range(1, len(items) + 1)):
        raise ValueError("atomic_ids must be consecutive and ordered")
    flattened: list[int] = []
    for item in items:
        if item.segment_ids != sorted(item.segment_ids) or len(set(item.segment_ids)) != len(item.segment_ids):
            raise ValueError(f"atomic item {item.atomic_id} has invalid segment_ids")
        if not item.raw_text.strip():
            raise ValueError(f"atomic item {item.atomic_id} has no raw_text")
        flattened.extend(item.segment_ids)
    if flattened != list(range(1, len(flattened) + 1)):
        raise ValueError("atomic inputs do not cover consecutive candidate segments exactly once")


def validate_atomic_items(
    atomic_inputs: list[AtomicItemInput], items: list[StructuredItem]
) -> None:
    if len(items) != len(atomic_inputs):
        raise ValueError("structured item count does not match atomic group count")
    for index, (source, item) in enumerate(zip(atomic_inputs, items), 1):
        if item.item_index != index:
            raise ValueError("structured item indexes are not consecutive")
        if item.raw_text != source.raw_text:
            raise ValueError(f"structured item {index} raw_text was modified")
        if is_generic_aggregate_title(item.title):
            raise ValueError(f'atomic item {index} has aggregate title "{item.title}"')
        children = aggregate_children(item)
        if children:
            raise ValueError(f"atomic item {index} still contains multiple nested items")
        data = item.structured_data
        for key in ("industries", "transaction_types"):
            value = data.get(key)
            if value is not None and not isinstance(value, list):
                raise ValueError(f"atomic item {index} field {key} must be an array")
        object_field = "extra_constraints" if item.detected_type == "buy_demand" else "extra_facts"
        value = data.get(object_field)
        if value is not None and not isinstance(value, dict):
            raise ValueError(f"atomic item {index} field {object_field} must be an object")


def expand_aggregate_items_locally(items: list[StructuredItem]) -> list[StructuredItem]:
    expanded: list[StructuredItem] = []
    for item in items:
        children = aggregate_children(item)
        aggregate = bool(children) or is_generic_aggregate_title(item.title)
        if not aggregate:
            expanded.append(item)
            continue
        segments = split_numbered_entries(item.raw_text)
        if len(segments) < 2:
            raise ValueError(
                f'aggregate item "{item.title}" cannot be split from its source text'
            )
        if len(children) != len(segments):
            expanded.extend(local_atomic_items(segments, item.classification_confidence))
            continue
        for segment, child in zip(segments, children):
            data = aggregate_base_data(item)
            data.update(normalize_atomic_child(child, item.detected_type))
            data.pop("description", None)
            title = child_title(child, segment)
            data["title"] = title
            data["summary"] = segment
            data["transaction_types"] = transaction_labels(segment)
            aggregate_industries = item.structured_data.get("industries")
            if isinstance(aggregate_industries, list):
                description = str(child.get("description", ""))
                industries = [
                    value for value in aggregate_industries
                    if isinstance(value, str) and (value in segment or value in description)
                ]
                if industries:
                    data["industries"] = industries
            if item.detected_type == "buy_demand":
                data["buyer_type"] = buyer_type_from_segment(segment)
            expanded.append(StructuredItem(
                item_index=len(expanded) + 1,
                raw_text=segment,
                detected_type=item.detected_type,
                classification_confidence=item.classification_confidence,
                title=title,
                structured_data=data,
            ))
    return [item.model_copy(update={"item_index": index}) for index, item in enumerate(expanded, 1)]


def local_atomic_items(segments: list[str], confidence: float) -> list[StructuredItem]:
    result: list[StructuredItem] = []
    for segment in segments:
        kind, local_confidence = classify_direction(segment)
        title = make_title(segment)
        data: dict[str, Any] = {
            "title": title,
            "summary": segment,
            "industries": extract_industries(segment),
            "transaction_types": extract_transaction_types(segment),
        }
        if kind == "buy_demand":
            data.update({
                "buyer_type": buyer_type_from_segment(segment),
                "target_types": [],
                "preferred_regions": extract_regions(segment),
                "preferred_stages": [],
                "extra_constraints": {"local_atomic_split": True},
            })
        elif kind == "sell_project":
            data.update({
                "project_types": [],
                "province": first_or_none(extract_regions(segment)),
                "extra_facts": {"local_atomic_split": True},
            })
        result.append(StructuredItem(
            item_index=len(result) + 1,
            raw_text=segment,
            detected_type=kind,
            classification_confidence=min(confidence, local_confidence),
            title=title,
            structured_data=data,
        ))
    return result


def aggregate_children(item: StructuredItem) -> list[dict[str, Any]]:
    paths = (
        ("extra_facts", "sub_projects"),
        ("extra_constraints", "sub_requirements"),
    )
    for parent, child in paths:
        container = item.structured_data.get(parent)
        values = container.get(child) if isinstance(container, dict) else None
        if isinstance(values, list) and len(values) > 1 and all(isinstance(value, dict) for value in values):
            return values
    return []


def aggregate_base_data(item: StructuredItem) -> dict[str, Any]:
    data = dict(item.structured_data)
    for parent, child in (("extra_facts", "sub_projects"), ("extra_constraints", "sub_requirements")):
        container = data.get(parent)
        if isinstance(container, dict) and child in container:
            cleaned = dict(container)
            cleaned.pop(child, None)
            data[parent] = cleaned
    # These aggregate values combine unrelated children and must not leak into each atomic item.
    atomic_fields = {
        "title", "summary", "industries", "transaction_types", "buyer_name", "buyer_type", "company_name",
        "financing_round", "financing_amount_min", "financing_amount_max", "revenue_min", "revenue_max",
        "net_profit_min", "net_profit_max", "valuation_min", "valuation_max", "pe_min", "pe_max",
        "province", "city", "listed_status", "transfer_ratio_min", "transfer_ratio_max",
        "investment_amount_min", "investment_amount_max", "target_revenue_min", "target_revenue_max",
        "target_net_profit_min", "target_net_profit_max", "target_valuation_min", "target_valuation_max",
        "listed_status_requirement", "control_ratio_min", "profitability_required",
    }
    for key in atomic_fields:
        data.pop(key, None)
    return data


def normalize_atomic_child(child: dict[str, Any], detected_type: str) -> dict[str, Any]:
    result = dict(child)
    if detected_type == "sell_project":
        aliases = {
            "net_profit": ("net_profit_min", "net_profit_max"),
            "revenue": ("revenue_min", "revenue_max"),
            "valuation": ("valuation_min", "valuation_max"),
            "valuation_pe": ("pe_min", "pe_max"),
        }
        for source, targets in aliases.items():
            value = result.pop(source, None)
            if value is not None:
                for target in targets:
                    result.setdefault(target, value)
        facts = {}
        for key in ("status", "exclude", "preferred_location", "preferred_themes"):
            if key in result:
                facts[key] = result.pop(key)
        if facts:
            result["extra_facts"] = facts
    else:
        constraints = {}
        for key in ("exclude", "preferred_location", "preferred_themes", "status"):
            if key in result:
                constraints[key] = result.pop(key)
        if constraints:
            result["extra_constraints"] = constraints
    return result


def transaction_labels(text: str) -> list[str]:
    return [value for value in ("融资", "并购", "收购", "转让", "出售", "投资") if value in text]


def buyer_type_from_segment(text: str) -> str | None:
    for value in ("国资集团", "国资", "上市公司", "产业基金", "基金", "实力买方", "买方", "资方"):
        if value in text:
            return value
    return None


def child_title(child: dict[str, Any], segment: str) -> str:
    for key in ("title", "project_name", "company_name", "buyer_name", "description"):
        value = child.get(key)
        if isinstance(value, str) and value.strip():
            return value.strip()[:80]
    return make_title(segment)


def is_generic_aggregate_title(title: str) -> bool:
    return bool(re.search(r"(?:多个|多项|若干|一批).{0,8}(?:项目|需求|标的|资产)", title.strip()))


def split_numbered_entries(text: str) -> list[str]:
    normalized = text.replace("\r\n", "\n").strip()
    marker = re.compile(r"(?<![\d.])(?=(?:\d{1,2}[.、])\s*[A-Za-z\u4e00-\u9fff])")
    parts = [part.strip() for part in marker.split(normalized) if part.strip()]
    return parts if len(parts) > 1 else []


def split_items(content: str) -> list[str]:
    text = content.replace("\r\n", "\n").strip()
    if not text:
        return []
    # Numbered top-level items are common in the initial data. Indented subconditions stay attached.
    marker = re.compile(r"(?m)^(?=(?:\d{1,2}[\.、]|[一二三四五六七八九十]+、)\s*)")
    parts = [part.strip() for part in marker.split(text) if part.strip()]
    if len(parts) > 1:
        return parts
    paragraphs = [p.strip() for p in re.split(r"\n\s*\n+", text) if p.strip()]
    return paragraphs or [text]


def fallback_structure(content: str) -> list[StructuredItem]:
    items: list[StructuredItem] = []
    for index, raw in enumerate(split_items(content), start=1):
        kind, confidence = classify_direction(raw)
        title = make_title(raw)
        data: dict[str, Any] = {
            "title": title,
            "summary": raw,
            "industries": extract_industries(raw),
            "transaction_types": extract_transaction_types(raw),
        }
        if kind == "buy_demand":
            data.update({"target_types": [], "preferred_regions": extract_regions(raw), "preferred_stages": [], "extra_constraints": {}})
        elif kind == "sell_project":
            data.update({"project_types": [], "province": first_or_none(extract_regions(raw)), "extra_facts": {}})
        items.append(StructuredItem(
            item_index=index,
            raw_text=raw,
            detected_type=kind,
            classification_confidence=confidence,
            title=title,
            structured_data=data,
        ))
    return items


def classify_direction(text: str) -> tuple[Literal["buy_demand", "sell_project", "unknown"], float]:
    if control_transfer_override(text):
        return "sell_project", 0.98
    if commodity_trade_override(text):
        return "unknown", 0.98
    if acquisition_buyer_override(text):
        return "buy_demand", 0.98
    buy_patterns = [
        r"拟.{0,8}(收购|并购|投资)",
        r"(?:上市公司|国资集团|国资|产业基金|基金|实力买方|买方|资方).{0,16}(?:收购|并购|投资|寻求|寻找)",
        r"寻求.{0,12}(标的|项目|壳|资源|订单合作)",
        r"寻找.{0,12}(标的|项目|企业)",
        r"需求净利润", r"收购方向", r"买方", r"资方收购", r"急需收购",
    ]
    sell_patterns = [r"寻求.{0,8}(并购|融资|收购)", r"接受.{0,8}并购", r"控股权转让", r"项目寻求", r"标的转让", r"出售", r"融资需求"]
    buy = sum(bool(re.search(pattern, text)) for pattern in buy_patterns)
    sell = sum(bool(re.search(pattern, text)) for pattern in sell_patterns)
    if buy > sell and buy > 0:
        return "buy_demand", min(0.6 + buy * 0.1, 0.9)
    if sell > buy and sell > 0:
        return "sell_project", min(0.6 + sell * 0.1, 0.9)
    return "unknown", 0.4


def make_title(text: str) -> str:
    line = re.sub(r"^\s*(?:\d{1,2}[\.、]|[一二三四五六七八九十]+、)\s*", "", text.splitlines()[0]).strip()
    return line[:80] or "未命名信息"


def extract_industries(text: str) -> list[str]:
    vocabulary = ["人工智能", "智能制造", "工业母机", "半导体", "医药", "医疗", "康养", "护理院", "康复医院", "低空经济", "固态电池", "储能", "激光雷达", "算力中心", "物联网", "新能源", "薄膜", "矿产", "文旅"]
    return [value for value in vocabulary if value in text]


def extract_transaction_types(text: str) -> list[str]:
    result: list[str] = []
    mapping = [("融资", "financing"), ("并购", "acquisition"), ("收购", "acquisition"), ("转让", "transfer"), ("直投", "direct_investment"), ("出售", "sale")]
    for keyword, value in mapping:
        if keyword in text and value not in result:
            result.append(value)
    return result


def extract_regions(text: str) -> list[str]:
    regions = ["广东", "浙江", "上海", "北京", "江苏", "苏州", "南京", "深圳", "长三角", "大湾区"]
    return [region for region in regions if region in text]


def first_or_none(values: list[str]) -> str | None:
    return values[0] if values else None
