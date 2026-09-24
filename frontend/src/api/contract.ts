import { getData, postData } from './request';
import type { Contract } from '../types';

export const contractApi = {
  list: () => getData<Contract[]>('/contracts'),
  detail: (id: number) => getData<Contract>(`/contracts/${id}`),
  sign: (id: number) => postData<Contract>(`/contracts/${id}/sign`),
  submitStage: (id: number, stageIndex: number, note: string) =>
    postData<Contract>(`/contracts/${id}/stages/submit`, { stageIndex, note }),
  confirmStage: (id: number, stageIndex: number) =>
    postData<Contract>(`/contracts/${id}/stages/confirm`, { stageIndex }),
  rejectStage: (id: number, stageIndex: number, reason: string) =>
    postData<Contract>(`/contracts/${id}/stages/reject`, { stageIndex, reason })
};
