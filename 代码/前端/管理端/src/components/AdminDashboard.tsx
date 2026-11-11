import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import {
  ArrowRight,
  Package,
  ClipboardCheck,
  PanelsTopLeft,
  Headphones,
  Plus,
  BookOpen,
  TrendingUp,
  LayoutDashboard,
  MessageSquare,
} from "lucide-react";
import { adminApi } from "../lib/admin-api";
import { GuideButton } from "./AdminGuide";
import { StateView } from "./shared";
import { publicSite } from "../lib/admin-ux";
export function AdminDashboard() {
  const [counts, setCounts] = useState<number[] | null>(null);
  const [error, setError] = useState<unknown>();
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true;
    setError(undefined);
    Promise.all(
      ["", "pending", "published", "draft"].map((status) =>
        adminApi.products(
          new URLSearchParams({ limit: "1", ...(status ? { status } : {}) }),
        ),
      ),
    )
      .then((rows) => {
        if (active) setCounts(rows.map((row) => row.total));
      })
      .catch((e) => {
        if (active) setError(e);
      });
    return () => {
      active = false;
    };
  }, [retry]);
  return (
    <>
      <div className="admin-heading">
        <div>
          <span className="eyebrow">清叶运营工作台</span>
          <h1>今天，要处理些什么？</h1>
          <p>上架商品、处理投稿、调整首页，都从这里开始。</p>
        </div>
        <Link className="button-white" to="/products?new=1">
          <Plus size={17} />
          上架新商品
        </Link>
      </div>
      {error ? (
        <StateView error={error} onRetry={() => setRetry(retry + 1)} />
      ) : (
        <div className="stat-grid">
          {[
            {
              label: "全部商品",
              detail: "查看与维护资料",
              to: "/products",
              icon: Package,
            },
            {
              label: "等待审核",
              detail: "核对会员提交的资料",
              to: "/reviews",
              icon: ClipboardCheck,
            },
            {
              label: "正在展示",
              detail: "用户可以看到的商品",
              to: "/products?status=published",
              icon: TrendingUp,
            },
            {
              label: "未发布草稿",
              detail: "继续填写或准备上架",
              to: "/products?status=draft",
              icon: BookOpen,
            },
          ].map((item, i) => (
            <Link
              className={`stat-card ${i === 1 ? "attention" : ""}`}
              key={item.label}
              to={item.to}
            >
              <div>
                <span>{item.label}</span>
                <item.icon size={19} />
              </div>
              <strong className="mono">
                {counts ? counts[i].toLocaleString("zh-CN") : "—"}
              </strong>
              <p>
                {item.detail}
                <ArrowRight size={14} />
              </p>
            </Link>
          ))}
        </div>
      )}
      <section className="welcome-panel">
        <div className="welcome-icon">
          <LayoutDashboard size={30} />
        </div>
        <div>
          <span className="eyebrow">不用记步骤，跟着页面操作</span>
          <h2>第一次上架？先看一个小演示。</h2>
          <p>从填写资料到确认上架，三步了解操作流程。演示不会修改真实商品。</p>
        </div>
        <GuideButton kind="product" label="开始演示" />
      </section>
      <div className="dashboard-columns">
        <section>
          <div className="section-heading">
            <h2>常用操作</h2>
            <span>选择你要做的事</span>
          </div>
          <div className="task-grid">
            {[
              {
                icon: MessageSquare,
                title: "接待在线咨询",
                desc: "开始接待，查看商品卡片并回复用户",
                to: "/chat",
                tag: "在线沟通",
              },
              {
                icon: Package,
                title: "管理商品",
                desc: "修改标题、报价、图片，或下架商品",
                to: "/products",
                tag: "商品维护",
              },
              {
                icon: ClipboardCheck,
                title: "审核会员投稿",
                desc: "检查资料后通过，或说明原因退回",
                to: "/reviews",
                tag: "投稿审核",
              },
              {
                icon: PanelsTopLeft,
                title: "装修首页",
                desc: "选择页面区域，修改文案和展示数量",
                to: "/settings",
                tag: "首页内容",
              },
              {
                icon: Headphones,
                title: "更换客服与二维码",
                desc: "修改联系方式、服务时间和咨询入口",
                to: "/contacts",
                tag: "联系入口",
              },
            ].map((item) => (
              <Link className="task-card panel" key={item.to} to={item.to}>
                <span className="task-icon">
                  <item.icon size={22} />
                </span>
                <span>
                  <small>{item.tag}</small>
                  <strong>{item.title}</strong>
                  <p>{item.desc}</p>
                </span>
                <ArrowRight size={18} />
              </Link>
            ))}
          </div>
        </section>
        <aside className="workflow-card panel">
          <span className="eyebrow">日常操作顺序</span>
          <h2>从资料到展示</h2>
          <ol>
            <li>
              <span>1</span>
              <div>
                <strong>先填写资料</strong>
                <p>标题、分类、报价填写清楚。</p>
              </div>
            </li>
            <li>
              <span>2</span>
              <div>
                <strong>再整理图片</strong>
                <p>第一张作封面，细节图依次排好。</p>
              </div>
            </li>
            <li>
              <span>3</span>
              <div>
                <strong>检查后上架</strong>
                <p>确认无误再公开，会员投稿先审核。</p>
              </div>
            </li>
          </ol>
          <Link className="text-button" to="/help">
            查看完整操作指南 <ArrowRight size={15} />
          </Link>
        </aside>
      </div>
      <div className="dashboard-foot">
        <span>当前数据来自测试站，保存会修改测试站内容。</span>
        {publicSite && (
          <a href={publicSite} target="_blank" rel="noreferrer">
            打开用户网站 ↗
          </a>
        )}
      </div>
    </>
  );
}
