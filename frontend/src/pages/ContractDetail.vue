<template>
  <div class="page">
    <el-page-header content="合同详情" @back="$router.push('/dashboard')" />
    <el-card v-if="contract" class="detail-card">
      <div class="c-head">
        <h2>{{ contract.contractNo }}</h2>
        <StatusBadge :status="contract.status" kind="contract" />
      </div>
      <p class="muted">需求：{{ contract.requirement?.title }}</p>
      <p><b>{{ formatCurrency(contract.totalAmount) }}</b>
        <span class="muted"> · {{ contract.paymentType === 'installments' ? '分阶段付款' : '一次性付款' }}</span>
      </p>
      <div class="c-parties">
        <span>甲方：{{ contract.partyA?.name }}</span>
        <span>乙方：{{ contract.partyB?.name }}</span>
      </div>
      <div class="actions">
        <el-button v-if="contract.status === 'pending_signature'" type="primary" @click="sign">签署确认</el-button>
        <el-tag v-if="contract.status === 'completed'" type="success">三段款项均已确认，合同已完成</el-tag>
      </div>
    </el-card>

    <el-card v-if="contract" class="stages-card">
      <template #header><b>阶段进度</b></template>
      <ProgressSteps :stages="contract.stages" />

      <el-divider />

      <div v-for="(stage, idx) in contract.stages" :key="stage.name" class="stage-row">
        <div class="stage-main">
          <div class="stage-title">
            <span class="stage-no">第 {{ idx + 1 }} 段</span>
            <b>{{ stage.name }}</b>
            <StatusBadge :status="stage.status" kind="stage" />
          </div>
          <div class="stage-meta muted">
            <span>{{ formatCurrency(stage.amount) }}</span>
            <span>· {{ stage.dueAt }}</span>
            <span v-if="stage.submittedAt">· 提交于 {{ formatTime(stage.submittedAt) }}</span>
            <span v-if="stage.paidAt">· 付款于 {{ formatTime(stage.paidAt) }}</span>
          </div>
          <el-alert
            v-if="stage.status === 'rejected' && stage.rejectReason"
            class="reject-alert"
            type="error"
            :closable="false"
            show-icon
            :title="`驳回原因：${stage.rejectReason}`"
          />
        </div>
        <div class="stage-ops">
          <template v-if="contract.status !== 'completed' && contract.status !== 'terminated'">
            <el-button
              v-if="isPartyB && (stage.status === 'in_progress' || stage.status === 'rejected')"
              type="primary"
              :loading="busyIdx === idx"
              @click="submit(idx)"
            >
              {{ stage.status === 'rejected' ? '重新提交交付' : '提交交付' }}
            </el-button>
            <template v-if="isPartyA && stage.status === 'submitted'">
              <el-button type="success" :loading="busyIdx === idx" @click="confirm(idx)">确认付款</el-button>
              <el-button type="danger" plain :disabled="busyIdx === idx" @click="reject(idx)">驳回</el-button>
            </template>
          </template>
        </div>
      </div>

      <p class="flow-tip muted">
        流程：乙方提交当前阶段 → 甲方确认付款后下一段才开始；交付不符时甲方可驳回，乙方整改后重新提交。最后一段确认付款后合同自动完成。
      </p>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import { ElMessage, ElMessageBox } from 'element-plus';
import type { Contract } from '../types';
import { useContractStore } from '../stores/contract';
import { useUserStore } from '../stores/user';
import { contractApi } from '../api/contract';
import StatusBadge from '../components/common/StatusBadge.vue';
import ProgressSteps from '../components/common/ProgressSteps.vue';
import { formatCurrency } from '../utils/formatCurrency';

const route = useRoute();
const store = useContractStore();
const userStore = useUserStore();
const contract = ref<Contract | null>(null);
const busyIdx = ref<number | null>(null);

const isPartyA = computed(() => contract.value?.partyAId === userStore.user?.id);
const isPartyB = computed(() => contract.value?.partyBId === userStore.user?.id);

async function load() {
  const id = Number(route.params.id);
  contract.value = await store.fetchDetail(id);
}

async function sign() {
  if (!contract.value) return;
  await contractApi.sign(contract.value.id);
  ElMessage.success('签署成功，第一段已开始');
  await load();
}

async function runStageAction(idx: number, action: () => Promise<Contract>, okText: string) {
  if (!contract.value) return;
  busyIdx.value = idx;
  try {
    const updated = await action();
    contract.value = updated;
    ElMessage.success(okText);
  } finally {
    busyIdx.value = null;
  }
}

function submit(idx: number) {
  if (!contract.value) return;
  const stage = contract.value.stages[idx];
  return runStageAction(
    idx,
    () => contractApi.submitStage(contract.value!.id, idx + 1),
    stage.status === 'rejected' ? '已重新提交，等待甲方确认' : '已提交，等待甲方确认付款'
  );
}

async function confirm(idx: number) {
  if (!contract.value) return;
  const isLast = idx === contract.value.stages.length - 1;
  await runStageAction(
    idx,
    () => contractApi.confirmStage(contract.value!.id, idx + 1),
    isLast ? '末段款项已确认，合同自动完成' : '已确认付款，下一段开始'
  );
}

async function reject(idx: number) {
  if (!contract.value) return;
  let reason = '';
  try {
    const { value } = await ElMessageBox.prompt('请说明交付不符的地方，乙方将据此整改', `驳回第 ${idx + 1} 段交付`, {
      confirmButtonText: '确认驳回',
      cancelButtonText: '取消',
      inputType: 'textarea',
      inputPlaceholder: '例如：功能未按约定实现 / 验收用例不通过',
      inputValidator: (v: string) => (v && v.trim() ? true : '请填写驳回原因')
    });
    reason = value;
  } catch {
    return; // user cancelled
  }
  await runStageAction(
    idx,
    () => contractApi.rejectStage(contract.value!.id, idx + 1, reason.trim()),
    '已驳回，等待乙方整改后重新提交'
  );
}

function formatTime(s: string): string {
  const d = new Date(s);
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString();
}

onMounted(() => void load());
</script>

<style scoped>
.detail-card { margin-bottom: 16px; }
.c-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
.c-parties { display: flex; gap: 24px; margin: 12px 0; }
.actions { margin-top: 16px; }
.stages-card { margin-bottom: 24px; }
.stage-row { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; padding: 12px 0; }
.stage-row + .stage-row { border-top: 1px solid var(--el-border-color-lighter); }
.stage-main { flex: 1; }
.stage-title { display: flex; align-items: center; gap: 8px; }
.stage-no { color: var(--el-color-primary); font-weight: 600; }
.stage-meta { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 6px; }
.stage-ops { flex-shrink: 0; }
.reject-alert { margin-top: 8px; }
.flow-tip { margin-top: 12px; font-size: 13px; }
</style>
