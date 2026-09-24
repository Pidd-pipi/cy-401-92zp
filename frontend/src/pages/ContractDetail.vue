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
      <el-alert
        v-if="contract.status === 'pending_signature'"
        title="合同待双方签署，签署后第一段才会开始。"
        type="info"
        :closable="false"
        show-icon
      />
      <el-alert
        v-else-if="contract.status === 'completed'"
        title="三段付款均已确认，合同已自动完成。"
        type="success"
        :closable="false"
        show-icon
      />
      <div class="actions">
        <el-button
          v-if="contract.status === 'pending_signature'"
          type="primary"
          :loading="busy"
          @click="sign"
        >签署确认</el-button>
      </div>
    </el-card>

    <el-card v-if="contract" class="stages-card">
      <template #header><b>阶段付款进度</b></template>
      <el-alert
        class="flow-tip"
        type="info"
        :closable="false"
        title="乙方提交当前阶段后，甲方确认付款下一段才会开始；未确认的阶段不能跳过。甲方可驳回不符的交付，乙方调整后重新提交。"
      />
      <ProgressSteps :stages="contract.stages" />

      <el-divider />

      <div class="stage-list">
        <div
          v-for="(stage, index) in contract.stages"
          :key="stage.name"
          class="stage-row"
          :class="{ active: stage.status === 'in_progress' || stage.status === 'submitted' || stage.status === 'rejected' }"
        >
          <div class="stage-main">
            <div class="stage-title">
              <b>第{{ index + 1 }}段 · {{ stage.name }}</b>
              <el-tag :type="stageTagType(stage.status)" size="small">{{ stageLabel(stage.status) }}</el-tag>
            </div>
            <div class="muted">{{ formatCurrency(stage.amount) }} · {{ stage.dueAt }}</div>
            <div v-if="stage.note" class="stage-note">交付说明：{{ stage.note }}</div>
            <div v-if="stage.status === 'rejected' && stage.rejectReason" class="stage-reject">
              驳回原因：{{ stage.rejectReason }}
            </div>
            <div v-if="stage.confirmedAt" class="muted">付款确认时间：{{ formatTime(stage.confirmedAt) }}</div>
          </div>
          <div class="stage-actions">
            <!-- 乙方：交付中或被驳回时可提交/重新提交当前段 -->
            <el-button
              v-if="isPartyB && (stage.status === 'in_progress' || stage.status === 'rejected') && contractActive"
              type="primary"
              :loading="busy"
              @click="openSubmit(index)"
            >{{ stage.status === 'rejected' ? '调整后重新提交' : '提交交付' }}</el-button>

            <!-- 甲方：乙方已提交，确认付款或驳回 -->
            <template v-if="isPartyA && stage.status === 'submitted' && contractActive">
              <el-button type="success" :loading="busy" @click="confirm(index)">确认付款</el-button>
              <el-button type="danger" plain :loading="busy" @click="openReject(index)">驳回</el-button>
            </template>
          </div>
        </div>
      </div>
    </el-card>

    <!-- 乙方提交交付 -->
    <el-dialog v-model="submitDialogVisible" title="提交阶段交付" width="480px">
      <p class="muted">第{{ submitIndex + 1 }}段 · {{ currentStage?.name }}</p>
      <el-input
        v-model="submitNote"
        type="textarea"
        :rows="4"
        placeholder="说明本次交付的内容、链接或备注（选填，最多 500 字）"
        maxlength="500"
        show-word-limit
      />
      <template #footer>
        <el-button @click="submitDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="busy" @click="submit">提交</el-button>
      </template>
    </el-dialog>

    <!-- 甲方驳回 -->
    <el-dialog v-model="rejectDialogVisible" title="驳回阶段交付" width="480px">
      <p class="muted">第{{ rejectIndex + 1 }}段 · {{ currentRejectStage?.name }}</p>
      <el-input
        v-model="rejectReason"
        type="textarea"
        :rows="4"
        placeholder="请说明交付不符的地方，便于乙方调整（必填，最多 500 字）"
        maxlength="500"
        show-word-limit
      />
      <template #footer>
        <el-button @click="rejectDialogVisible = false">取消</el-button>
        <el-button type="danger" :loading="busy" @click="reject">确认驳回</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import { ElMessage, ElMessageBox } from 'element-plus';
import type { Contract } from '../types';
import { StageStatusLabel } from '../types/enums';
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
const busy = ref(false);

const isPartyA = computed(() => contract.value?.partyAId === userStore.user?.id);
const isPartyB = computed(() => contract.value?.partyBId === userStore.user?.id);
const contractActive = computed(
  () => contract.value?.status === 'in_progress' || contract.value?.status === 'pending_review'
);

const stageLabel = (status: string) => StageStatusLabel[status] || status;

function stageTagType(status: string): 'success' | 'warning' | 'danger' | 'info' | 'primary' {
  switch (status) {
    case 'done':
      return 'success';
    case 'submitted':
      return 'warning';
    case 'rejected':
      return 'danger';
    case 'in_progress':
      return 'primary';
    default:
      return 'info';
  }
}

function formatTime(value?: string): string {
  if (!value) return '';
  return new Date(value).toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  });
}

async function load() {
  const id = Number(route.params.id);
  contract.value = await store.fetchDetail(id);
}

async function sign() {
  if (!contract.value) return;
  busy.value = true;
  try {
    await contractApi.sign(contract.value.id);
    ElMessage.success('签署成功，第一段已开始');
    await load();
  } finally {
    busy.value = false;
  }
}

// 乙方提交交付
const submitDialogVisible = ref(false);
const submitIndex = ref(0);
const submitNote = ref('');
const currentStage = computed(() => contract.value?.stages[submitIndex.value]);

function openSubmit(index: number) {
  submitIndex.value = index;
  submitNote.value = contract.value?.stages[index]?.note || '';
  submitDialogVisible.value = true;
}

async function submit() {
  if (!contract.value) return;
  busy.value = true;
  try {
    await contractApi.submitStage(contract.value.id, submitIndex.value, submitNote.value.trim());
    ElMessage.success('已提交，等待甲方确认付款');
    submitDialogVisible.value = false;
    await load();
  } finally {
    busy.value = false;
  }
}

// 甲方确认付款
async function confirm(index: number) {
  if (!contract.value) return;
  try {
    await ElMessageBox.confirm(
      `确认已支付第${index + 1}段款项「${contract.value.stages[index].name}」？确认后下一段才会开始。`,
      '确认付款',
      { type: 'warning', confirmButtonText: '确认付款', cancelButtonText: '再想想' }
    );
  } catch {
    return;
  }
  busy.value = true;
  try {
    const updated = await contractApi.confirmStage(contract.value.id, index);
    ElMessage.success(index === updated.stages.length - 1 ? '末段已确认，合同自动完成' : '付款已确认，下一段已开始');
    await load();
  } finally {
    busy.value = false;
  }
}

// 甲方驳回
const rejectDialogVisible = ref(false);
const rejectIndex = ref(0);
const rejectReason = ref('');
const currentRejectStage = computed(() => contract.value?.stages[rejectIndex.value]);

function openReject(index: number) {
  rejectIndex.value = index;
  rejectReason.value = '';
  rejectDialogVisible.value = true;
}

async function reject() {
  if (!contract.value) return;
  if (!rejectReason.value.trim()) {
    ElMessage.warning('请填写驳回原因');
    return;
  }
  busy.value = true;
  try {
    await contractApi.rejectStage(contract.value.id, rejectIndex.value, rejectReason.value.trim());
    ElMessage.success('已驳回，等待乙方调整后重新提交');
    rejectDialogVisible.value = false;
    await load();
  } finally {
    busy.value = false;
  }
}

onMounted(() => void load());
</script>

<style scoped>
.detail-card { margin-bottom: 16px; }
.c-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
.c-parties { display: flex; gap: 24px; margin: 12px 0; }
.actions { margin-top: 16px; }
.stages-card { margin-bottom: 24px; }
.flow-tip { margin-bottom: 20px; }
.stage-list { display: flex; flex-direction: column; gap: 12px; }
.stage-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  padding: 12px 16px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 8px;
}
.stage-row.active {
  border-color: var(--el-color-primary-light-5);
  background: var(--el-color-primary-light-9);
}
.stage-main { display: flex; flex-direction: column; gap: 4px; }
.stage-title { display: flex; align-items: center; gap: 8px; }
.stage-note { font-size: 13px; }
.stage-reject { font-size: 13px; color: var(--el-color-danger); }
.stage-actions { display: flex; gap: 8px; flex-shrink: 0; }
</style>
