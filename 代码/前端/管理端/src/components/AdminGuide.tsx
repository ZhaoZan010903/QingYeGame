import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  ArrowRight,
  Check,
  CircleHelp,
  Play,
  RotateCcw,
  X,
} from "lucide-react";

export type DemoKind = "product" | "home" | "contact" | "review" | "chat";
export const guides = {
  chat: {
    title: "接待商品咨询",
    to: "/chat",
    where: "管理后台 · 在线咨询 / 用户站 · 消息",
    steps: ["点击开始接待", "查看商品和留言", "回复用户并结束咨询"],
    notes: [
      "进入在线咨询，点击开始接待。系统在在线且有空闲接待位的管理员里随机分配。",
      "选择一条咨询，商品卡片和历史消息会一起显示。每人最多同时接待 3 个咨询。",
      "输入回复并发送。沟通完成后点击结束咨询，释放一个接待位置。暂停接待仍可回复已有对话。",
    ],
  },
  product: {
    title: "上架一个商品",
    to: "/products?new=1",
    where: "账号集市 · 商品详情",
    steps: ["填写商品资料", "整理商品图片", "检查并确认上架"],
    notes: [
      "按顺序填写发布类型、标题、区服等账号资料和报价。出售无需选择交易分类；租赁需设置时日或周月标签。编号自动生成，私密资料单独填写。",
      "在真实编辑页上传图片，第一张是封面。可调整顺序、重试失败的图片。",
      "平台商品可以确认后直接上架；会员投稿需要经过审核。",
      "商品管理按页浏览，可按类型、来源、状态查找。删除会移入回收站，恢复后先进入草稿。首次上架时间不会被重新上架覆盖。",
    ],
  },
  home: {
    title: "修改首页内容",
    to: "/settings",
    where: "首页 · 热门榜单",
    steps: ["找到修改位置", "修改标题和数量", "保存并查看首页"],
    notes: [
      "进入首页装修，选择“热门榜单”。选中区域会高亮。",
      "修改标题和展示数量。这里调整展示，推荐排序到“热门榜单”中管理。",
      "保存后刷新用户站。轮播可查看实时电脑和手机预览；商品区示意不代表实际榜单。",
    ],
  },
  contact: {
    title: "更换客服二维码",
    to: "/contacts",
    where: "用户点击“联系客服”后的弹窗",
    steps: ["选择需要修改的客服", "更换联系方式", "选择入口并保存"],
    notes: [
      "在客服与二维码中选择一位已有客服，也可以新增。",
      "填写微信或 QQ 等联系方式，并选择新的二维码图片。",
      "选择购买、租赁或发布协助等入口。二维码上传后还需要保存才生效。",
    ],
  },
  review: {
    title: "审核会员投稿",
    to: "/reviews",
    where: "通过后出现在账号集市",
    steps: ["打开待审核商品", "核对资料与全部图片", "通过或退回修改"],
    notes: [
      "进入待审核，打开一条投稿。核对标题、账号属性和报价，租赁商品在账号资料中选择时日或周月标签。",
      "切换到商品图片，查看完整图集。演示不代表实际完成验号。",
      "内容齐全可通过并上架；缺少资料时填写原因，再退回修改。",
    ],
  },
} as const;

/** Deliberately isolated: the teaching sandbox imports no transport or upload API. */
export function DemoContent({
  kind,
  onClose,
}: {
  kind: DemoKind;
  onClose?: () => void;
}) {
  const guide = guides[kind];
  const [step, setStep] = useState(0);
  const [title, setTitle] = useState(
    kind === "home"
      ? "热门榜单"
      : kind === "contact"
        ? "清叶客服"
        : "QQ 炫舞 · 演示账号",
  );
  const [done, setDone] = useState(false);
  return (
    <div className="guide-content">
      <div className="guide-intro">
        <span className="eyebrow">跟着做一次</span>
        <h2>{guide.title}</h2>
        <p>这是独立的操作练习，所有输入只在当前演示中使用。</p>
      </div>
      <ol className="stepper">
        {guide.steps.map((name, i) => (
          <li
            key={name}
            className={i === step ? "active" : i < step ? "done" : ""}
          >
            <button
              type="button"
              onClick={() => {
                setStep(i);
                setDone(false);
              }}
            >
              <span>{i < step ? <Check size={14} /> : i + 1}</span>
              {name}
            </button>
          </li>
        ))}
      </ol>
      <div className="demo-stage">
        <div className="demo-instructions">
          <span className="pill">第 {step + 1} 步 / 3</span>
          <h3>{guide.steps[step]}</h3>
          <p>{guide.notes[step]}</p>
          <div className="location-note">
            <strong>前台位置</strong>
            <span>{guide.where}</span>
          </div>
        </div>
        <div className="demo-sandbox">
          <div className="demo-window-bar">
            <i />
            <i />
            <i />
            <span>操作示例 · 不会保存到网站</span>
          </div>
          {step < 2 ? (
            <>
              <label className="form-field">
                {kind === "home"
                  ? "区块标题"
                  : kind === "contact"
                    ? "客服名称"
                    : "商品标题"}
                <input
                  value={title}
                  maxLength={60}
                  onChange={(e) => setTitle(e.target.value)}
                />
              </label>
              <div className="demo-result">
                <span>显示效果</span>
                <strong>{title || "请填写标题"}</strong>
                {kind === "product" || kind === "review" ? (
                  <>
                    <div className="demo-image-slots">
                      <span>封面图</span>
                      <span>细节图</span>
                      <span>更多图片</span>
                    </div>
                    <small>图片仅为位置示意，不上传文件。</small>
                  </>
                ) : (
                  <p>
                    {kind === "home"
                      ? "商品 01　 商品 02　 商品 03"
                      : "微信 / QQ　·　服务时间　·　二维码位置"}
                  </p>
                )}
              </div>
            </>
          ) : (
            <div className="demo-confirm">
              <span className="demo-check">
                <Check size={24} />
              </span>
              <h3>{done ? "练习完成" : "最后检查一下"}</h3>
              <p>{title}</p>
              <p>
                {done
                  ? "你已经了解整个流程，可以到实际页面操作。"
                  : "点击下面的按钮，体验操作完成的提示。"}
              </p>
              <button
                type="button"
                className="button-white"
                disabled={done}
                onClick={() => setDone(true)}
              >
                {done
                  ? "已完成演示"
                  : kind === "review"
                    ? "模拟审核通过"
                    : "模拟保存"}
              </button>
            </div>
          )}
        </div>
      </div>
      <div className="guide-controls">
        <button
          type="button"
          className="text-button"
          onClick={() => {
            setStep(0);
            setDone(false);
          }}
        >
          <RotateCcw size={15} />
          重新演示
        </button>
        <div>
          <button
            type="button"
            className="button-outline"
            disabled={step === 0}
            onClick={() => {
              setStep(step - 1);
              setDone(false);
            }}
          >
            上一步
          </button>
          {step < 2 ? (
            <button
              type="button"
              className="button-white"
              onClick={() => setStep(step + 1)}
            >
              下一步 <ArrowRight size={16} />
            </button>
          ) : (
            <Link className="button-white" to={guide.to} onClick={onClose}>
              去实际操作 <ArrowRight size={16} />
            </Link>
          )}
        </div>
      </div>
    </div>
  );
}
export function GuideButton({
  kind,
  label = "看操作演示",
}: {
  kind: DemoKind;
  label?: string;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (open) dialog.current?.showModal();
    else dialog.current?.close();
  }, [open]);
  return (
    <>
      <button
        ref={trigger}
        type="button"
        className="button-outline guide-trigger"
        onClick={() => setOpen(true)}
      >
        <Play size={15} />
        {label}
      </button>
      <dialog
        ref={dialog}
        className="guide-dialog"
        aria-label={`${guides[kind].title}操作演示`}
        onCancel={() => setOpen(false)}
        onClose={() => {
          setOpen(false);
          trigger.current?.focus();
        }}
      >
        <button
          type="button"
          className="icon-button dialog-close"
          aria-label="关闭演示"
          onClick={() => setOpen(false)}
        >
          <X size={20} />
        </button>
        {open && <DemoContent kind={kind} onClose={() => setOpen(false)} />}
      </dialog>
    </>
  );
}
export function AdminHelp() {
  const [kind, setKind] = useState<DemoKind>("product");
  return (
    <>
      <div className="admin-heading">
        <div>
          <span className="eyebrow">操作指南</span>
          <h1>第一次用，从这里开始</h1>
          <p>选择要做的事情，跟着示例走一遍。</p>
        </div>
        <CircleHelp size={30} />
      </div>
      <div className="help-tabs">
        {Object.entries(guides).map(([key, item]) => (
          <button
            className={key === kind ? "active" : ""}
            key={key}
            onClick={() => setKind(key as DemoKind)}
          >
            {item.title}
          </button>
        ))}
      </div>
      <section className="panel">
        <DemoContent key={kind} kind={kind} />
      </section>
      <div className="help-faq panel">
        <h3>几个常用操作</h3>
        <details>
          <summary>改了商品资料，为什么前台看不到了？</summary>
          <p>
            修改已上架商品后会重新变成草稿。检查资料和图片，再重新发布或提交审核，用户才能看到新版本。
          </p>
        </details>
        <details>
          <summary>热门榜单和首页轮播有什么区别？</summary>
          <p>
            热门榜单根据有效浏览、收藏和去重咨询人数计算自然热度，再加入管理员为单个商品设置的推荐加分；轮播由管理员在首页装修中手工设置，独立编辑文案、图片及位置并实时预览。
          </p>
        </details>
        <details>
          <summary>保存与上架有什么区别？</summary>
          <p>
            商品保存草稿只存资料，上架才会公开。首页装修保存后刷新生效；客服和说明页还需要处于启用或公开状态。
          </p>
        </details>
      </div>
    </>
  );
}
