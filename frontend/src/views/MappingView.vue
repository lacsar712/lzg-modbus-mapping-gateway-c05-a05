<template>
  <div class="card-panel">
    <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:14px;gap:12px;flex-wrap:wrap">
      <div>
        <h2 style="margin:0 0 4px;font-size:18px">映射配置</h2>
        <div class="sub" style="margin:0">候选 YAML 先出结构化 Diff，可试运行校验，确认后才会应用；非法配置不会落地，旧配置持续生效</div>
      </div>
      <el-button :loading="loading" @click="load">重新加载生效配置</el-button>
    </div>

    <el-steps :active="step - 1" align-center finish-status="success" style="margin-bottom:16px">
      <el-step title="编辑候选" description="修改 YAML 并生成 Diff" />
      <el-step title="Diff 预览 / 试运行" description="核对变更，可选 dry-run" />
      <el-step title="确认应用" description="热加载，失败保留旧配置" />
    </el-steps>

    <!-- 步骤 1：编辑 -->
    <template v-if="step === 1">
      <el-input
        v-model="yamlText"
        type="textarea"
        :rows="22"
        class="mono"
        :disabled="!auth.canWrite"
        placeholder="mapping YAML"
      />
      <div style="display:flex;gap:8px;margin-top:12px;align-items:center;flex-wrap:wrap">
        <el-button type="primary" :disabled="!auth.canWrite" :loading="previewing" @click="onPreview">
          生成 Diff 预览
        </el-button>
        <el-button v-if="yamlText !== activeYaml" @click="yamlText = activeYaml">放弃修改</el-button>
        <span v-if="yamlText !== activeYaml" style="color:var(--warn);font-size:12px">编辑器内容与生效配置不一致</span>
        <span v-else style="color:var(--muted);font-size:12px">编辑器内容即当前生效配置</span>
      </div>
      <el-alert
        v-if="previewError"
        :title="previewError"
        type="error"
        show-icon
        style="margin-top:12px"
        :closable="false"
      >
        <template #default>
          <div>{{ previewError }}（旧配置仍在生效，候选未落地）</div>
          <el-button size="small" style="margin-top:6px" @click="restoreActive">恢复生效配置文本</el-button>
        </template>
      </el-alert>
    </template>

    <!-- 步骤 2：Diff 预览 + 试运行 + 确认 -->
    <template v-else>
      <div style="display:flex;gap:8px;flex-wrap:wrap;margin-bottom:12px">
        <el-tag type="success">设备 +{{ diff.summary.devicesAdded }}</el-tag>
        <el-tag type="danger">设备 -{{ diff.summary.devicesRemoved }}</el-tag>
        <el-tag type="warning">设备改 {{ diff.summary.devicesChanged }}</el-tag>
        <el-tag type="success">点位 +{{ diff.summary.pointsAdded }}</el-tag>
        <el-tag type="danger">点位 -{{ diff.summary.pointsRemoved }}</el-tag>
        <el-tag type="warning">点位改 {{ diff.summary.pointsChanged }}</el-tag>
      </div>

      <el-alert
        v-if="diff.identical"
        title="候选与当前生效配置完全一致，没有需要应用的变更"
        type="info"
        :closable="false"
        show-icon
        style="margin-bottom:12px"
      />

      <el-table v-if="diff.entries.length" :data="diff.entries" size="small" border class="diff-table">
        <el-table-column label="类型" width="108">
          <template #default="{ row }">
            <el-tag :type="opMeta(row.op).type" size="small">{{ opMeta(row.op).label }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="deviceId" label="设备" min-width="120" />
        <el-table-column label="点位" min-width="130">
          <template #default="{ row }">{{ row.point || '—' }}</template>
        </el-table-column>
        <el-table-column label="变化（旧 → 新）" min-width="280">
          <template #default="{ row }">
            <div v-for="ch in row.changes" :key="ch.field" class="chg-line">
              <span class="chg-field">{{ fieldLabel(ch.field) }}</span>
              <span class="chg-old">{{ fmtVal(ch.old) }}</span>
              <span class="chg-arrow">→</span>
              <span class="chg-new">{{ fmtVal(ch.new) }}</span>
            </div>
          </template>
        </el-table-column>
      </el-table>

      <div style="margin:14px 0;display:flex;gap:8px;align-items:center;flex-wrap:wrap">
        <el-button :loading="dryRunning" @click="onDryRun">试运行 Dry-run 校验</el-button>
        <span v-if="dryReport" style="font-size:13px">
          <el-tag v-if="dryReport.valid" type="success" size="small">校验通过</el-tag>
          <el-tag v-else type="danger" size="small">校验失败</el-tag>
          <span style="color:var(--muted);margin-left:8px">
            设备 {{ dryReport.deviceCount }} / 点位 {{ dryReport.pointCount }} / 合并读窗口 {{ dryReport.windows?.length || 0 }}
          </span>
        </span>
      </div>

      <el-alert v-if="dryError" :title="dryError" type="error" :closable="false" show-icon style="margin-bottom:12px">
        <template #default>
          <div>{{ dryError }}（试运行不写入，旧配置仍在生效）</div>
        </template>
      </el-alert>

      <el-table v-if="dryReport?.valid && dryReport.windows?.length" :data="dryReport.windows" size="small" border style="margin-bottom:14px">
        <el-table-column prop="deviceId" label="设备" min-width="140" />
        <el-table-column prop="start" label="起始寄存器" width="120" />
        <el-table-column prop="count" label="寄存器数量" width="120" />
      </el-table>

      <el-alert
        v-if="applyError"
        :title="applyError"
        type="error"
        show-icon
        :closable="false"
        style="margin-bottom:12px"
      >
        <template #default>
          <div>应用失败，已保留旧配置。</div>
          <el-button size="small" style="margin-top:6px" @click="restoreActive">恢复生效配置文本</el-button>
        </template>
      </el-alert>

      <div style="display:flex;gap:8px">
        <el-button @click="backToEdit">返回修改</el-button>
        <el-button
          type="danger"
          :disabled="!auth.canWrite || diff.identical"
          :loading="applying"
          @click="onApply"
        >
          确认应用（热加载）
        </el-button>
      </div>
    </template>
  </div>
</template>

<script setup>
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../api/client'
import { useAuthStore } from '../stores/auth'

const auth = useAuthStore()
const loading = ref(false)
const previewing = ref(false)
const dryRunning = ref(false)
const applying = ref(false)

const step = ref(1)
const activeYaml = ref('')
const yamlText = ref('')
const diff = ref({ summary: {}, entries: [], identical: true })
const dryReport = ref(null)
const previewError = ref('')
const dryError = ref('')
const applyError = ref('')

const FIELD_LABELS = {
  name: '名称', endpoint: 'Endpoint', unitId: 'Unit ID', timeoutMs: '超时(ms)',
  address: '地址', type: '类型', bit: '位', writable: '可写',
  scale: '量程', offset: '偏移', min: '下限', max: '上限'
}

const OP_META = {
  add: { label: '设备新增', type: 'success' },
  remove: { label: '设备删除', type: 'danger' },
  change: { label: '设备变更', type: 'warning' },
  'point-add': { label: '点位新增', type: 'success' },
  'point-remove': { label: '点位删除', type: 'danger' },
  'point-change': { label: '点位变更', type: 'warning' }
}

function fieldLabel(f) {
  return FIELD_LABELS[f] || f
}
function opMeta(op) {
  return OP_META[op] || { label: op, type: 'info' }
}
function fmtVal(v) {
  if (v === null || v === undefined || v === '') return '—（未设置）'
  if (v === true) return '是'
  if (v === false) return '否'
  if (typeof v === 'number') {
    return Number.isInteger(v) ? String(v) : String(Math.round(v * 1e6) / 1e6)
  }
  return String(v)
}

async function load() {
  loading.value = true
  try {
    const { data } = await api.get('/mapping')
    activeYaml.value = data.yaml || ''
    yamlText.value = activeYaml.value
    step.value = 1
    previewError.value = ''
    dryReport.value = null
    dryError.value = ''
    applyError.value = ''
  } catch (e) {
    ElMessage.error(e.response?.data?.error || '加载失败')
  } finally {
    loading.value = false
  }
}

async function onPreview() {
  previewing.value = true
  previewError.value = ''
  try {
    const { data } = await api.post('/mapping/preview', { yaml: yamlText.value })
    diff.value = data.diff
    dryReport.value = null
    dryError.value = ''
    applyError.value = ''
    step.value = 2
  } catch (e) {
    previewError.value = e.response?.data?.error || 'Diff 预览失败'
  } finally {
    previewing.value = false
  }
}

async function onDryRun() {
  dryRunning.value = true
  dryError.value = ''
  dryReport.value = null
  try {
    const { data } = await api.post('/mapping/dry-run', { yaml: yamlText.value })
    dryReport.value = data.report
  } catch (e) {
    dryError.value = e.response?.data?.error || '试运行失败'
    if (e.response?.data?.report) dryReport.value = e.response.data.report
  } finally {
    dryRunning.value = false
  }
}

function backToEdit() {
  step.value = 1
  applyError.value = ''
}

function restoreActive() {
  yamlText.value = activeYaml.value
  previewError.value = ''
  applyError.value = ''
  dryError.value = ''
  step.value = 1
}

async function onApply() {
  try {
    await ElMessageBox.confirm(
      `将应用变更：设备 +${diff.value.summary.devicesAdded}/-${diff.value.summary.devicesRemoved}/改${diff.value.summary.devicesChanged}，点位 +${diff.value.summary.pointsAdded}/-${diff.value.summary.pointsRemoved}/改${diff.value.summary.pointsChanged}。确认热加载？`,
      '确认应用映射',
      { type: 'warning', confirmButtonText: '确认应用', cancelButtonText: '取消' }
    )
  } catch {
    return
  }
  applying.value = true
  applyError.value = ''
  try {
    const { data } = await api.post('/reload', { yaml: yamlText.value })
    activeYaml.value = data.yaml || yamlText.value
    yamlText.value = activeYaml.value
    step.value = 1
    ElMessage.success(`应用成功，设备数 ${data.devices}`)
  } catch (e) {
    applyError.value = e.response?.data?.error || '应用失败'
    if (e.response?.data?.yaml) activeYaml.value = e.response.data.yaml
  } finally {
    applying.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.diff-table :deep(.el-table__cell) {
  vertical-align: top;
}
.chg-line {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12.5px;
  line-height: 1.9;
  flex-wrap: wrap;
}
.chg-field {
  color: var(--muted);
  min-width: 72px;
}
.chg-old {
  color: #ff8c8c;
  background: rgba(255, 90, 90, 0.1);
  padding: 0 6px;
  border-radius: 4px;
}
.chg-new {
  color: #7ee2b8;
  background: rgba(62, 207, 142, 0.1);
  padding: 0 6px;
  border-radius: 4px;
}
.chg-arrow {
  color: var(--muted);
}
</style>
