from app.main import (
    AtomicGroup,
    AtomicItemInput,
    StructuredItem,
    build_atomic_inputs,
    candidate_segments,
    classify_direction,
    expand_aggregate_items_locally,
    fallback_structure,
    normalize_structured_item,
    parse_model_json,
    grouping_chunks,
    split_numbered_entries,
    structure_chunks,
    validate_atomic_items,
    validate_group_coverage,
)


def test_buy_direction():
    kind, _ = classify_direction("上市医药公司拟收购iPSC干细胞项目，没有盈利要求")
    assert kind == "buy_demand"


def test_sell_direction():
    kind, _ = classify_direction("人工智能平台B轮融资需求，估值10亿，拟融1亿")
    assert kind == "sell_project"


def test_project_acting_as_listed_company_acquirer_is_buy_demand():
    raw = "超级优质人工智能项目寻求借壳收购上市公司控制权，要求一次性拿到60%以上股票"
    kind, confidence = classify_direction(raw)
    assert kind == "buy_demand"
    assert confidence >= 0.95


def test_control_transfer_is_sell_project():
    for raw in (
        "医药上市公司控转（一）：优先胶囊类标的",
        "医药上市公司溢价20%转让控制权",
    ):
        kind, confidence = classify_direction(raw)
        assert kind == "sell_project"
        assert confidence >= 0.95


def test_normalize_preserves_series_marker_for_duplicate_safety():
    raw = "医药上市公司控转（一）：优先胶囊类标的"
    item = normalize_structured_item({
        "raw_text": raw,
        "detected_type": "sell_project",
        "classification_confidence": 0.9,
        "title": "医药上市公司控制权转让",
        "structured_data": {"transaction_types": ["控股权转让"]},
    }, 1)
    assert "（一）" in item["title"]


def test_ore_trade_purchase_is_unknown():
    kind, confidence = classify_direction("矿石贸易：收国内矿石、铜石、重晶石和铅锌石")
    assert kind == "unknown"
    assert confidence >= 0.95


def test_normalize_overrides_model_buy_classification_for_ore_trade():
    raw = "收国内矿石（金石，铜石，重晶石，铅锌石）"
    item = normalize_structured_item({
        "raw_text": raw,
        "detected_type": "buy_demand",
        "classification_confidence": 0.9,
        "title": "矿石采购需求",
        "structured_data": {"transaction_types": ["采购"], "target_types": ["矿石"]},
    }, 1)
    assert item["detected_type"] == "unknown"
    assert item["structured_data"] == {"title": "矿石采购需求", "summary": raw}


def test_normalize_overrides_wrong_sell_schema_for_active_acquirer():
    raw = "人工智能项目寻求借壳收购A股公司控制权，要求拿到60%以上股份"
    item = normalize_structured_item({
        "raw_text": raw,
        "detected_type": "sell_project",
        "classification_confidence": 0.9,
        "title": "人工智能项目借壳",
        "structured_data": {
            "project_types": ["项目"],
            "transfer_ratio_min": 0.6,
            "extra_facts": {"参考模式": "借壳"},
        },
    }, 1)
    assert item["detected_type"] == "buy_demand"
    assert item["structured_data"]["control_ratio_min"] == 0.6
    assert item["structured_data"]["target_types"] == ["上市公司"]
    assert item["structured_data"]["extra_constraints"]["参考模式"] == "借壳"
    assert "extra_facts" not in item["structured_data"]


def test_batch_split():
    items = fallback_structure("1. 上市公司拟收购固态电池项目\n2. 智能制造项目寻求并购")
    assert len(items) == 2
    assert items[0].detected_type == "buy_demand"
    assert items[1].detected_type == "sell_project"


def test_normalize_nullable_model_fields():
    item = normalize_structured_item({
        "item_index": None,
        "raw_text": "某上市公司寻找医疗项目",
        "detected_type": "buy_demand",
        "classification_confidence": None,
        "title": None,
        "structured_data": None,
    }, 1)
    assert item["title"] == "某上市公司寻找医疗项目"
    assert item["classification_confidence"] == 0.5
    assert item["structured_data"] == {}
    assert item["item_index"] == 1


def test_parse_model_json_repairs_missing_colon():
    parsed = parse_model_json(
        '{"items":[{"item_index":1,"raw_text":"测试项目","title" "测试标题"}]}'
    )
    assert parsed["items"][0]["title"] == "测试标题"


def test_parse_model_json_keeps_valid_json():
    parsed = parse_model_json('```json\n{"items": []}\n```')
    assert parsed == {"items": []}


def test_locally_expands_multiple_sell_projects():
    item = StructuredItem(
        item_index=1,
        raw_text="1、IDC液冷项目寻求并购 2、稀有金属矿寻求并购。",
        detected_type="sell_project",
        classification_confidence=0.9,
        title="多个项目寻求并购",
        structured_data={
            "industries": ["IDC", "矿产"],
            "extra_facts": {"sub_projects": [
                {"description": "IDC液冷项目", "net_profit": 35000000},
                {"description": "稀有金属矿", "net_profit": 800000000},
            ]},
        },
    )
    result = expand_aggregate_items_locally([item])
    assert [value.title for value in result] == ["IDC液冷项目", "稀有金属矿"]
    assert result[0].raw_text == "1、IDC液冷项目寻求并购"
    assert "sub_projects" not in result[0].structured_data["extra_facts"]
    assert result[0].structured_data["industries"] == ["IDC"]
    assert result[0].structured_data["net_profit_min"] == 35000000
    assert result[0].structured_data["net_profit_max"] == 35000000
    assert result[0].structured_data["transaction_types"] == ["并购"]


def test_locally_expands_multiple_buy_demands():
    item = StructuredItem(
        item_index=1,
        raw_text="1、国资收购汽车零部件企业 2、上市公司并购半导体项目 3、买方收购IDC项目",
        detected_type="buy_demand",
        classification_confidence=0.9,
        title="多个收购需求",
        structured_data={
            "target_revenue_min": 500000000,
            "target_net_profit_min": 50000000,
            "extra_constraints": {"sub_requirements": [
            {"description": "汽车零部件企业", "target_net_profit_min": 50000000},
            {"description": "半导体项目"},
            {"description": "IDC项目"},
        ]}},
    )
    result = expand_aggregate_items_locally([item])
    assert len(result) == 3
    assert result[2].title == "IDC项目"
    assert result[0].structured_data["buyer_type"] == "国资"
    assert result[1].structured_data["transaction_types"] == ["并购"]
    assert result[0].structured_data["target_net_profit_min"] == 50000000
    assert "target_net_profit_min" not in result[1].structured_data
    assert "target_revenue_min" not in result[2].structured_data


def test_split_numbered_entries_does_not_split_decimal():
    assert split_numbered_entries("净利润1.4亿左右的智能制造项目寻求并购") == []


def test_locally_expands_generic_aggregate_without_children():
    item = StructuredItem(
        item_index=1,
        raw_text=(
            "1.上市公司现金收购IPO撤材料硬科技项目。 "
            "2.上市公司寻求算力订单合作。 "
            "3.人工智能项目寻求借壳收购上市公司控制权。"
        ),
        detected_type="buy_demand",
        classification_confidence=0.9,
        title="多项收购需求",
        structured_data={"industries": ["人工智能"]},
    )
    result = expand_aggregate_items_locally([item])
    assert len(result) == 3
    assert result[0].detected_type == "buy_demand"
    assert result[2].detected_type == "buy_demand"
    assert all("多项收购需求" != value.title for value in result)
    assert result[2].structured_data["extra_constraints"]["local_atomic_split"] is True


def test_child_count_mismatch_falls_back_to_local_atomic_split():
    item = StructuredItem(
        item_index=1,
        raw_text="1、上市公司收购医疗项目 2、国资收购半导体企业 3、买方收购IDC项目",
        detected_type="buy_demand",
        classification_confidence=0.9,
        title="收购需求汇总",
        structured_data={"extra_constraints": {"sub_requirements": [
            {"description": "医疗项目"},
            {"description": "半导体企业"},
        ]}},
    )
    result = expand_aggregate_items_locally([item])
    assert len(result) == 3
    assert all(value.detected_type == "buy_demand" for value in result)


def test_candidate_segments_keep_offsets_and_decimal():
    content = "项目净利润1.4亿元。\n1、行业：医疗\n2、地区：上海"
    segments = candidate_segments(content)
    assert len(segments) == 3
    assert segments[0].raw_text == "项目净利润1.4亿元。"
    assert [segment.segment_id for segment in segments] == [1, 2, 3]
    assert all(
        content[segment.start_offset:segment.end_offset] == segment.raw_text
        for segment in segments
    )


def test_build_atomic_inputs_uses_original_source_slice():
    content = "某集团拟收购护理院\n1、收购方向：护理院\n2、营收要求：5000万元\n\n3、IDC项目寻求并购"
    segments = candidate_segments(content)
    groups = [
        AtomicGroup(group_index=1, segment_ids=[1, 2, 3], reason="同一需求条件"),
        AtomicGroup(group_index=2, segment_ids=[4], reason="独立项目"),
    ]
    items = build_atomic_inputs(content, segments, groups)
    assert len(items) == 2
    assert items[0].raw_text == "某集团拟收购护理院\n1、收购方向：护理院\n2、营收要求：5000万元"
    assert items[1].raw_text == "3、IDC项目寻求并购"


def test_group_coverage_rejects_missing_segment():
    segments = candidate_segments("1、项目甲\n2、项目乙\n3、项目丙")
    groups = [
        AtomicGroup(group_index=1, segment_ids=[1], reason="独立项目"),
        AtomicGroup(group_index=2, segment_ids=[3], reason="独立项目"),
    ]
    try:
        validate_group_coverage(segments, groups)
        assert False, "expected coverage validation to fail"
    except ValueError as error:
        assert "coverage mismatch" in str(error)


def test_atomic_validation_rejects_aggregate_title():
    sources = [AtomicItemInput(atomic_id=1, segment_ids=[1], raw_text="项目甲寻求并购")]
    items = [StructuredItem(
        item_index=1,
        raw_text="项目甲寻求并购",
        detected_type="sell_project",
        classification_confidence=0.9,
        title="多个项目寻求并购",
        structured_data={"extra_facts": {}},
    )]
    try:
        validate_atomic_items(sources, items)
        assert False, "expected atomic validation to fail"
    except ValueError as error:
        assert "aggregate title" in str(error)


def test_candidate_segments_support_nbsp_and_numeric_content():
    content = "1.\u00a0算力中心项目，净利1.1亿。 2.\u00a0主板医疗公司控股权转让。"
    segments = candidate_segments(content)
    assert len(segments) == 2
    assert segments[0].raw_text.startswith("1.\u00a0算力中心")
    assert segments[1].raw_text.startswith("2.\u00a0主板医疗")


def test_candidate_segments_split_number_followed_by_number_but_not_decimal():
    content = "1、净利润3500万项目 2、8亿利润的矿产项目，净利润1.4亿"
    segments = candidate_segments(content)
    assert len(segments) == 2
    assert segments[1].raw_text == "2、8亿利润的矿产项目，净利润1.4亿"


def test_grouping_chunks_preserve_blocks_and_soft_limit():
    content = "\n\n".join(
        ["标题A\n1、条件A\n2、条件B"]
        + [f"{index}、独立项目{index}" for index in range(3, 15)]
    )
    segments = candidate_segments(content)
    chunks = grouping_chunks(segments, max_segments=4)
    assert [segment.segment_id for segment in chunks[0]][:3] == [1, 2, 3]
    assert len(chunks[0]) <= 4
    assert all(len({segment.block_index for segment in chunk}) >= 1 for chunk in chunks)
    flattened = [segment.segment_id for chunk in chunks for segment in chunk]
    assert flattened == list(range(1, len(segments) + 1))


def test_structure_chunks_cover_all_atomic_items():
    items = [
        AtomicItemInput(atomic_id=index, segment_ids=[index], raw_text=f"项目{index}")
        for index in range(1, 24)
    ]
    chunks = structure_chunks(items, max_items=10)
    assert [len(chunk) for chunk in chunks] == [10, 10, 3]
    assert [item.atomic_id for chunk in chunks for item in chunk] == list(range(1, 24))
