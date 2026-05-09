// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { CopyIcon, HardDrive, MemoryStick } from 'lucide-react';

import PieceGrid from '@/components/piece-grid';
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Progress } from '@/components/ui/progress';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { formatBytes, formatNumber } from '@/lib/format-utils';
import type { Torrent, TorrentStats } from '@/lib/types/api';

const StatGridItem = ({ label, value }: { label: string, value: string | number }) => (
  <div className='space-y-1 rounded-md border p-3'>
    <div className='text-xs text-muted-foreground'>{label}</div>
    <div className='text-base font-semibold'>{value}</div>
  </div>
);

interface TorrentStatsDialogLayoutProps {
  torrent: Torrent | null,
  open: boolean,
  onOpenChange: (open: boolean) => void,
  stats: TorrentStats | null,
  loading: boolean,
  error: string | null,
  handleCopy: (value: string, label: string) => void
}

export function TorrentStatsDialogLayout({
  torrent,
  open,
  onOpenChange,
  stats,
  loading,
  error,
  handleCopy,
}: TorrentStatsDialogLayoutProps) {
  if (!torrent) return null;

  const completionPercentage = (stats && stats.totalSize > 0)
    ? (stats.completedSize / stats.totalSize) * 100
    : 0;

  return (
    <Dialog open={open}
      onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Statistics</DialogTitle>
          <DialogDescription className='break-all'>
            {torrent.title || 'Untitled'}
          </DialogDescription>
        </DialogHeader>

        <div className='space-y-6 max-h-[70vh] overflow-y-auto pr-3 -mr-3'>
          {error && <div className='p-3 bg-destructive/10 text-destructive rounded-lg text-sm'>{error}</div>}

          {loading && !stats && <div className='text-center py-8 text-muted-foreground'>Loading statistics...</div>}

          <div className='space-y-3'>
            <h4 className='text-sm font-medium'>Torrent Info</h4>
            <div className='grid grid-cols-2 gap-2'>
              <Button onClick={() => handleCopy(torrent.magnet, 'Magnet Link')}
                className='gap-2'>
                <CopyIcon className='h-4 w-4' />
                Magnet Link
              </Button>
              <Button disabled
                variant='outline'
                className='gap-2'>
                {torrent.storage === 'file' ? (
                  <HardDrive className='h-4 w-4' />
                ) : (
                  <MemoryStick className='h-4 w-4' />
                )}
                <span className='hidden md:inline'>Storage:</span> <span className='capitalize'>{torrent.storage}</span>
              </Button>
            </div>
          </div>

          {stats && (
            <>
              <div className='space-y-3'>
                <h4 className='text-sm font-medium'>Overview</h4>
                <div className='grid grid-cols-2 sm:grid-cols-3 gap-4'>
                  <StatGridItem label='Total Size'
                    value={formatBytes(stats.totalSize)} />
                  <StatGridItem label='Completed'
                    value={formatBytes(stats.completedSize)} />
                  <StatGridItem label='In Memory'
                    value={formatBytes(stats.inMemorySize)} />
                </div>
              </div>

              <div className='space-y-3'>
                <h4 className='text-sm font-medium'>Completion Progress</h4>
                <div className='space-y-2'>
                  <div className='flex items-center justify-between text-sm'>
                    <span className='text-muted-foreground'>Completion</span>
                    <span className='font-medium'>{completionPercentage.toFixed(1)}%</span>
                  </div>
                  <Progress value={completionPercentage}
                    className='h-2' />
                </div>
              </div>

              <div className='space-y-3'>
                <h4 className='text-sm font-medium'>Piece Activity</h4>
                <div className='grid grid-cols-2 sm:grid-cols-3 gap-4'>
                  <StatGridItem label='Total Pieces'
                    value={formatNumber(stats.totalPieces)} />
                  <StatGridItem label='Completed'
                    value={formatNumber(stats.piecesComplete)} />
                  <StatGridItem label='In Memory'
                    value={formatNumber(stats.inMemory)} />
                </div>
              </div>

              {stats.pieces && stats.pieces.length > 0 && (
                <div className='space-y-3'>
                  <h4 className='text-sm font-medium'>Piece Map</h4>
                  <PieceGrid
                    totalPieces={stats.totalPieces}
                    pieces={stats.pieces}
                  />
                </div>
              )}

              <div className='space-y-3'>
                <h4 className='text-sm font-medium'>Peer & Seeder Activity</h4>
                <div className='grid grid-cols-2 sm:grid-cols-3 gap-4'>
                  <StatGridItem label='Total Peers'
                    value={formatNumber(stats.totalPeers)} />
                  <StatGridItem label='Active Peers'
                    value={formatNumber(stats.activePeers)} />
                  <StatGridItem label='Pending'
                    value={formatNumber(stats.pendingPeers)} />
                  <StatGridItem label='Half Open'
                    value={formatNumber(stats.halfOpenPeers)} />
                  <StatGridItem label='Seeders'
                    value={formatNumber(stats.connectedSeeders)} />
                </div>
              </div>

              <div className='space-y-3'>
                <h4 className='text-sm font-medium'>Torrent Memory Usage</h4>
                <div className='space-y-2'>
                  <div className='flex items-center justify-between text-sm'>
                    <span className='text-muted-foreground'>Usage Percentage</span>
                    <span className='font-medium'>{stats.memoryUsagePercentage.toFixed(2)}%</span>
                  </div>
                  <Progress value={stats.memoryUsagePercentage}
                    className='h-2' />
                </div>
              </div>

              <Accordion type='multiple'
                className='w-full space-y-3'>
                <AccordionItem value='data-transfer'>
                  <AccordionTrigger className='text-sm font-medium'>Data Transfer Details</AccordionTrigger>
                  <AccordionContent className='pt-3'>
                    <div className='grid grid-cols-1 md:grid-cols-2 gap-6'>
                      <div className='space-y-3'>
                        <h5 className='text-xs font-semibold'>Data Read</h5>
                        <div className='space-y-2'>
                          <StatGridItem label='Total'
                            value={formatBytes(stats.bytesRead)} />
                          <StatGridItem label='Data'
                            value={formatBytes(stats.bytesReadData)} />
                          <StatGridItem label='Useful'
                            value={formatBytes(stats.bytesReadUsefulData)} />
                          <StatGridItem label='Useful Intended'
                            value={formatBytes(stats.bytesReadUsefulIntendedData)} />
                        </div>
                      </div>
                      <div className='space-y-3'>
                        <h5 className='text-xs font-semibold'>Data Written</h5>
                        <div className='space-y-2'>
                          <StatGridItem label='Total'
                            value={formatBytes(stats.bytesWritten)} />
                          <StatGridItem label='Data'
                            value={formatBytes(stats.bytesWrittenData)} />
                        </div>
                      </div>
                    </div>
                  </AccordionContent>
                </AccordionItem>

                <AccordionItem value='chunk-transfer'>
                  <AccordionTrigger className='text-sm font-medium'>Chunk Transfer Details</AccordionTrigger>
                  <AccordionContent className='pt-3'>
                    <div className='grid grid-cols-2 sm:grid-cols-3 gap-4'>
                      <StatGridItem label='Read'
                        value={formatNumber(stats.chunksRead)} />
                      <StatGridItem label='Useful'
                        value={formatNumber(stats.chunksReadUseful)} />
                      <StatGridItem label='Wasted'
                        value={formatNumber(stats.chunksReadWasted)} />
                      <StatGridItem label='Written'
                        value={formatNumber(stats.chunksWritten)} />
                    </div>
                  </AccordionContent>
                </AccordionItem>

                <AccordionItem value='hashing'>
                  <AccordionTrigger className='text-sm font-medium'>Hashing & Verification</AccordionTrigger>
                  <AccordionContent className='pt-3'>
                    <div className='grid grid-cols-2 sm:grid-cols-3 gap-4'>
                      <StatGridItem label='Bytes Hashed'
                        value={formatBytes(stats.bytesHashed)} />
                      <StatGridItem label='Good'
                        value={formatNumber(stats.piecesDirtiedGood)} />
                      <StatGridItem label='Bad'
                        value={formatNumber(stats.piecesDirtiedBad)} />
                    </div>
                  </AccordionContent>
                </AccordionItem>

                {stats.pieces && stats.totalPieces > 0 && stats.totalPieces <= 100 && (
                  <AccordionItem value='pieces'>
                    <AccordionTrigger className='text-sm font-medium'>
                      Detailed Pieces ({stats.totalPieces})
                    </AccordionTrigger>
                    <AccordionContent>
                      <div className='overflow-x-auto'>
                        <Table>
                          <TableHeader>
                            <TableRow>
                              <TableHead>Index</TableHead>
                              <TableHead>Size</TableHead>
                              <TableHead>Complete</TableHead>
                              <TableHead>In Memory</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {stats.pieces
                              .sort((a, b) => a.index - b.index)
                              .map(piece => (
                                <TableRow key={piece.index}>
                                  <TableCell>{piece.index}</TableCell>
                                  <TableCell>{formatBytes(piece.size)}</TableCell>
                                  <TableCell>{piece.complete ? 'Yes' : 'No'}</TableCell>
                                  <TableCell>{piece.inMemory ? 'Yes' : 'No'}</TableCell>
                                </TableRow>
                              ))}
                          </TableBody>
                        </Table>
                      </div>
                    </AccordionContent>
                  </AccordionItem>
                )}
              </Accordion>
            </>
          )}
          {!stats && !loading && !error && (
            <div className='text-center py-8 text-muted-foreground'>No piece statistics available</div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
