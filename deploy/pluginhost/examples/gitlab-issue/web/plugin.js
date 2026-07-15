// gitlab-issue demo plugin · Web Component 入口。
//
// 加载方式:
//   1. ongrid pluginhost server 暴露 /api/pluginhost/plugins/{id}/assets/*
//   2. web 端 pluginLoader 拉这个 JS → blob URL → <script> 注入
//   3. JS 全局注册 customElements.define("gitlab-issue-form", GitLabIssueForm)
//   4. pluginLoader 等到 customElements.get("gitlab-issue-form") 出现
//      → document.createElement → appendChild 到容器
//
// 设计:
//   - 用 Shadow DOM(attachShadow({mode:"open"}))隔离 CSS,避免污染 ongrid
//     主前端(.record/2026-07-15-pluginhost-loader-schema-form.md §已知限制提
//     到 Phase 6+ 才上 iframe sandbox,这里先做 Shadow DOM 这一层)
//   - 内部用原生 form + 简单校验;不依赖任何外部 UI 库
//   - 提交事件 detail 字段 shape = plugin capability 的 JSON Schema "params"。
//     父页面(<plugin-detail>)接 detail 后,通过 schemaform 的 onSubmit
//     把 params 透传给 /api/pluginhost/.../invoke。
//   - 不真调 GitLab API——这是 demo,真 API 调用在 server 端的 SubprocessRuntime
//     里(它会拉这个 binary 的 subprocess 走 stdio JSON-RPC)。

(function () {
  'use strict';

  const TAG = 'gitlab-issue-form';

  // schema 与 plugin.json 的 capabilities[0].schema 对齐,作为占位文档用。
  // 真实运行期 web 端用 SchemaForm 渲表单,这里组件主要承担"展示 + 提
  // 交事件"职责,内嵌一个简化版 input UI,方便直接嵌到 /plugins/:id 详情页。
  const SCHEMA = {
    project_id: { type: 'string', required: true, label: 'Project ID' },
    title:      { type: 'string', required: true, label: 'Issue Title' },
    body:       { type: 'string', required: false, label: 'Issue Body', multiline: true },
  };

  class GitLabIssueForm extends HTMLElement {
    constructor() {
      super();
      this.attachShadow({ mode: 'open' });
      this._render();
    }

    static get observedAttributes() {
      return ['project-id', 'title', 'disabled', 'theme'];
    }

    connectedCallback() {
      this._render();
      this.shadowRoot
        .querySelector('form')
        .addEventListener('submit', this._onSubmit.bind(this));
    }

    attributeChangedCallback() {
      this._render();
    }

    _render() {
      const disabled = this.hasAttribute('disabled');
      const presetProject = this.getAttribute('project-id') || '';
      const presetTitle = this.getAttribute('title') || '';

      this.shadowRoot.innerHTML = `
        <style>
          :host { display: block; font: 13px/1.5 system-ui, -apple-system, sans-serif; }
          .card {
            border: 1px solid #e5e7eb;
            border-radius: 8px;
            padding: 16px;
            background: #ffffff;
            color: #1f2937;
          }
          :host([theme="dark"]) .card {
            background: #18181b;
            border-color: #3f3f46;
            color: #f4f4f5;
          }
          h3 { margin: 0 0 12px; font-size: 14px; font-weight: 600; }
          .row { display: flex; flex-direction: column; gap: 4px; margin-bottom: 12px; }
          label { font-size: 12px; font-weight: 500; }
          label .req { color: #ef4444; margin-left: 2px; }
          input, textarea {
            width: 100%;
            border: 1px solid #d4d4d8;
            border-radius: 6px;
            padding: 6px 8px;
            box-sizing: border-box;
            font: inherit;
            background: transparent;
            color: inherit;
          }
          :host([theme="dark"]) input,
          :host([theme="dark"]) textarea { border-color: #52525b; }
          textarea { font-family: monospace; resize: vertical; min-height: 80px; }
          button {
            padding: 6px 14px;
            border-radius: 6px;
            border: none;
            background: #4f46e5;
            color: white;
            font-weight: 500;
            cursor: pointer;
          }
          button[disabled] { opacity: 0.5; cursor: not-allowed; }
          .meta { font-size: 11px; opacity: 0.7; margin-top: 12px; }
        </style>
        <div class="card">
          <h3>GitLab Issue · create</h3>
          <form novalidate>
            ${this._renderField(SCHEMA.project_id, presetProject, disabled)}
            ${this._renderField(SCHEMA.title, presetTitle, disabled)}
            ${this._renderField(SCHEMA.body, '', disabled)}
            <div style="text-align: right;">
              <button type="submit" ${disabled ? 'disabled' : ''}>提交</button>
            </div>
          </form>
          <div class="meta">
            demo plugin · ai.tool:create_issue · params 走 submit event 的 detail
          </div>
        </div>
      `;
    }

    _renderField(spec, value, disabled) {
      const id = 'f-' + spec.label.toLowerCase().replace(/\s+/g, '-');
      const required = spec.required
        ? '<span class="req">*</span>'
        : '';
      const valueAttr = spec.multiline
        ? ''
        : `value="${escapeAttr(value)}"`;
      const inputHtml = spec.multiline
        ? `<textarea id="${id}" name="${escapeAttr(spec.label)}"${
            disabled ? ' disabled' : ''
          }>${escapeHtml(value || '')}</textarea>`
        : `<input type="text" id="${id}" name="${escapeAttr(spec.label)}"${
            valueAttr
          }${disabled ? ' disabled' : ''} />`;
      return `
        <div class="row">
          <label for="${id}">${escapeHtml(spec.label)}${required}</label>
          ${inputHtml}
        </div>
      `;
    }

    _onSubmit(e) {
      e.preventDefault();
      const form = e.currentTarget;
      const get = (name) =>
        form.querySelector(`[name="${name}"]`).value.trim();

      const project_id = get('Project ID');
      const title = get('Issue Title');
      const body = get('Issue Body');

      // 校验失败:前端先反馈,不让 event 出去
      if (!project_id || !title) {
        const ev = new CustomEvent('invalid', {
          detail: { reason: 'project_id and title are required' },
        });
        this.dispatchEvent(ev);
        return;
      }

      // 触发自定义 submit 事件,detail = plugin capability params
      const submit = new CustomEvent('plugin-invoke', {
        bubbles: true,
        composed: true,
        detail: {
          capability: 'create_issue',
          params: { project_id, title, body },
        },
      });
      this.dispatchEvent(submit);
    }
  }

  if (!customElements.get(TAG)) {
    customElements.define(TAG, GitLabIssueForm);
  }

  // ----- utils ----------------------------------------------------------

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;');
  }

  function escapeAttr(s) {
    return String(s).replace(/"/g, '&quot;');
  }
})();
