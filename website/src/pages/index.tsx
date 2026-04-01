import clsx from 'clsx';
import Layout from '@theme/Layout';
import Link from '@docusaurus/Link';
import styles from './index.module.css';

const cards = [
  {
    title: '给项目成员',
    body: '从申请 Harbor 账号、给镜像打 tag、执行 docker push，到知道同步状态，文档按真实使用路径组织。',
    to: '/docs/02-user-guide',
  },
  {
    title: '给运维团队',
    body: '包含 .run 安装、systemd 托管、Caddy 入口、webhook、agent、通知队列与回调设计。',
    to: '/docs/03-ops-guide',
  },
  {
    title: '给排障人员',
    body: '任务状态、agent 连接、通知限流、callback、同仓库双 robot 凭据等常见问题都有直接的排障入口。',
    to: '/docs/07-troubleshooting',
  },
];

export default function Home(): JSX.Element {
  return (
    <Layout
      title="Harbor Relay"
      description="Open-source Harbor image relay, routing, callbacks, and notifications"
    >
      <header className={styles.hero}>
        <div className="container">
          <div className={styles.heroInner}>
            <div className={styles.heroCopy}>
              <p className={styles.eyebrow}>Image Sync Control Plane</p>
              <h1>把 Harbor 镜像同步做成可路由、可观测、可维护的服务</h1>
              <p className={styles.lead}>
                Harbor Relay 适合一套 Harbor 对接多个项目、多个目标环境和多个通知渠道的交付场景。
                它负责把 webhook、任务、agent、回调和通知串成一条稳定可追踪的链路。
              </p>
              <div className={styles.actions}>
                <Link className="button button--primary button--lg" to="/docs/intro">
                  开始阅读
                </Link>
                <Link className="button button--secondary button--lg" to="/docs/03-ops-guide">
                  查看部署手册
                </Link>
              </div>
            </div>
            <div className={styles.heroPanel}>
              <div className={styles.panelTitle}>核心链路</div>
              <div className={styles.flowLine}>Harbor Webhook</div>
              <div className={styles.flowArrow}>↓</div>
              <div className={styles.flowLine}>Relay Routing</div>
              <div className={styles.flowArrow}>↓</div>
              <div className={styles.flowLine}>Remote Agent</div>
              <div className={styles.flowArrow}>↓</div>
              <div className={styles.flowLine}>Target Registry</div>
              <div className={styles.flowArrow}>↓</div>
              <div className={styles.flowLine}>Callback / Notification</div>
            </div>
          </div>
        </div>
      </header>

      <main className={styles.main}>
        <section className="container">
          <div className={styles.cardGrid}>
            {cards.map((card) => (
              <Link key={card.title} className={clsx(styles.card)} to={card.to}>
                <h2>{card.title}</h2>
                <p>{card.body}</p>
              </Link>
            ))}
          </div>
        </section>
      </main>
    </Layout>
  );
}
