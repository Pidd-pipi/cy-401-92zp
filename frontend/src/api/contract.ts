import { getData, postData } from './request';
import type { Contract } from '../types';

export const contractApi = {
  list: () => getData<Contract[]>('/contracts'),
  detail: (id: number) => getData<Contract>(`/contracts/${id}`),
  sign: (id: number) => postData<Contract>(`/contracts/${id}/sign`),
  submitStage: (id: number, stageNo: number) =>
    postData<Contract>(`/contracts/${id}/stages/${stageNo}/submit`),
  confirmStage: (id: number, stageNo: number) =>
    postData<Contract>(`/contracts/${id}/stages/${stageNo}/confirm`),
  rejectStage: (id: number, stageNo: number, reason: string) =>
    postData<Contract>(`/contracts/${id}/stages/${stageNo}/reject`, { reason })
};
